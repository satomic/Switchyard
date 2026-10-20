// Package respstore holds the Responses API's server-side state: stored responses (continued with
// `previous_response_id`, read back with GET /v1/responses/{id}), items (referenced with
// `item_reference`), and conversations (the Conversations API, and `conversation` on a request).
//
// Why the router keeps this itself: it converts every Responses request to chat completions before
// routing, and the next turn of a conversation may go to a different model on a different
// connection. No upstream ever holds the state a Responses client expects the server to hold.
// VS Code Copilot Chat (0.71, BYOK on the Responses API) depends on it: it answers a first turn
// normally and continues with `previous_response_id` plus only the new items.
//
// Everything is stored in Responses form -- the items exactly as a client sent or received them,
// each with its id -- not in the converted chat form. That is what lets an item be referenced by
// id, input items be listed back, and the history be converted afresh for whichever model the next
// turn is routed to.
//
//	data/responses/<resp id>.json        {user, created_at, response, input_items, context}
//	data/conversations/<conv id>.json    {user, created_at, metadata, items}
//	data/responses/items/<item id>       the response or conversation holding that item
//
// `context` is the whole history a response ends: the items before it, its own input, its output.
// The instructions are not part of it: the Responses API does not carry them from one response to
// the next.
//
// Every record belongs to the user whose API key created it and is "not found" for anyone else.
// Responses and conversations expire after TTL of inactivity; expired files are pruned as new
// ones are written. Several workers sharing data/ see each other's records; two workers appending
// to one conversation at the same instant is not guarded against.
package respstore

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/satomic/model-router/server-go/internal/omap"
)

// TTL is how long a response or conversation lives without being used. OpenAI keeps stored
// responses for 30 days; a coding session is hours, and a month of full agent transcripts on a
// router's disk is a lot of data to keep for nobody.
const TTL = 7 * 24 * time.Hour

const pruneEvery = 200

var (
	ErrNotFound = errors.New("not found")
	idPattern   = regexp.MustCompile(`^[A-Za-z]+_[A-Za-z0-9]{8,64}$`)
)

// ValidID reports whether id has the shape of an id this router issues -- the guard that keeps a
// client-supplied id from ever becoming a path outside data/.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// NewID mints an id with the given prefix ("resp", "conv", "msg", ...).
func NewID(prefix string) string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(buf)
}

// Response is a stored response.
type Response struct {
	ID         string
	User       string
	CreatedAt  time.Time
	Response   *omap.Map // the response object as returned to the client
	InputItems []any     // this request's own input items, with ids
	Context    []any     // the whole history this response ends, in Responses form
}

// Conversation is a stored conversation.
type Conversation struct {
	ID        string
	User      string
	CreatedAt time.Time
	Metadata  *omap.Map
	Items     []any
}

type Store struct {
	root string
	// mu serialises read-modify-write of a conversation. countMu guards the prune counter on its
	// own, because it is bumped from inside sections that already hold mu.
	mu      sync.Mutex
	countMu sync.Mutex
	writes  int
}

func New(dataDir string) *Store { return &Store{root: dataDir} }

var (
	activeMu sync.RWMutex
	active   *Store
)

// Use sets the process-wide store; nil switches server-side state off.
func Use(s *Store) { activeMu.Lock(); active = s; activeMu.Unlock() }

func Active() *Store { activeMu.RLock(); defer activeMu.RUnlock(); return active }

func (s *Store) responsePath(id string) string { return filepath.Join(s.root, "responses", id+".json") }
func (s *Store) convPath(id string) string     { return filepath.Join(s.root, "conversations", id+".json") }
func (s *Store) itemPath(id string) string     { return filepath.Join(s.root, "responses", "items", id) }

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readDoc reads a record file into an ordered map (so items come back in the same form the rest
// of the router reads), or nil.
func readDoc(path string) *omap.Map {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	value, err := omap.FromJSON(data)
	if err != nil {
		return nil
	}
	doc, _ := value.(*omap.Map)
	return doc
}

func expired(doc *omap.Map) bool {
	stamp, err := time.Parse(time.RFC3339Nano, doc.Str("touched_at"))
	return err != nil || time.Since(stamp) > TTL
}

func cloneItems(items []any) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = omap.CloneValue(item)
	}
	return out
}

func (s *Store) indexItems(owner string, items []any) {
	for _, entry := range items {
		if item, ok := entry.(*omap.Map); ok {
			if id := item.Str("id"); ValidID(id) {
				_ = writeFile(s.itemPath(id), []byte(owner))
			}
		}
	}
}

func (s *Store) afterWrite() {
	s.countMu.Lock()
	s.writes++
	prune := s.writes%pruneEvery == 1
	s.countMu.Unlock()
	if prune {
		go s.prune()
	}
}

// -- Responses -----------------------------------------------------------------

// PutResponse stores a response. Failures are logged, not returned: the answer has already been
// delivered, and only a later continuation of it would be affected.
func (s *Store) PutResponse(r *Response) {
	if !ValidID(r.ID) {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	doc := omap.New()
	doc.Set("user", r.User)
	doc.Set("created_at", r.CreatedAt.UTC().Format(time.RFC3339Nano))
	doc.Set("touched_at", now)
	doc.Set("response", r.Response)
	doc.Set("input_items", r.InputItems)
	doc.Set("context", r.Context)
	data, err := json.Marshal(doc)
	if err == nil {
		err = writeFile(s.responsePath(r.ID), data)
	}
	if err != nil {
		log.Printf("WARNING mr: could not store response %s: %v", r.ID, err)
		return
	}
	s.indexItems(r.ID, r.InputItems)
	if output, ok := r.Response.Value("output").([]any); ok {
		s.indexItems(r.ID, output)
	}
	s.afterWrite()
}

// GetResponse returns a stored response that belongs to user.
func (s *Store) GetResponse(id, user string) (*Response, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	doc := readDoc(s.responsePath(id))
	if doc == nil || doc.Str("user") != user || expired(doc) {
		return nil, ErrNotFound
	}
	created, _ := time.Parse(time.RFC3339Nano, doc.Str("created_at"))
	return &Response{
		ID: id, User: user, CreatedAt: created,
		Response:   doc.Map("response"),
		InputItems: doc.Slice("input_items"),
		Context:    doc.Slice("context"),
	}, nil
}

func (s *Store) DeleteResponse(id, user string) error {
	if _, err := s.GetResponse(id, user); err != nil {
		return err
	}
	return os.Remove(s.responsePath(id))
}

// -- Items ---------------------------------------------------------------------

// Item finds an item by id among the user's stored responses and conversations.
func (s *Store) Item(id, user string) (*omap.Map, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	owner, err := os.ReadFile(s.itemPath(id))
	if err != nil {
		return nil, ErrNotFound
	}
	var lists [][]any
	if conv, err := s.GetConversation(string(owner), user); err == nil {
		lists = append(lists, conv.Items)
	} else if resp, err := s.GetResponse(string(owner), user); err == nil {
		output, _ := resp.Response.Value("output").([]any)
		lists = append(lists, resp.InputItems, output)
	} else {
		return nil, ErrNotFound
	}
	for _, list := range lists {
		for _, entry := range list {
			if item, ok := entry.(*omap.Map); ok && item.Str("id") == id {
				return item.Clone(), nil
			}
		}
	}
	return nil, ErrNotFound
}

// -- Conversations -------------------------------------------------------------

func (s *Store) saveConversation(c *Conversation) error {
	doc := omap.New()
	doc.Set("user", c.User)
	doc.Set("created_at", c.CreatedAt.UTC().Format(time.RFC3339Nano))
	doc.Set("touched_at", time.Now().UTC().Format(time.RFC3339Nano))
	doc.Set("metadata", c.Metadata)
	doc.Set("items", c.Items)
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	if err := writeFile(s.convPath(c.ID), data); err != nil {
		return err
	}
	s.indexItems(c.ID, c.Items)
	s.afterWrite()
	return nil
}

func (s *Store) CreateConversation(user string, metadata *omap.Map, items []any) (*Conversation, error) {
	if metadata == nil {
		metadata = omap.New()
	}
	c := &Conversation{ID: NewID("conv"), User: user, CreatedAt: time.Now(), Metadata: metadata, Items: items}
	s.mu.Lock()
	defer s.mu.Unlock()
	return c, s.saveConversation(c)
}

func (s *Store) GetConversation(id, user string) (*Conversation, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	doc := readDoc(s.convPath(id))
	if doc == nil || doc.Str("user") != user || expired(doc) {
		return nil, ErrNotFound
	}
	created, _ := time.Parse(time.RFC3339Nano, doc.Str("created_at"))
	metadata := doc.Map("metadata")
	if metadata == nil {
		metadata = omap.New()
	}
	return &Conversation{ID: id, User: user, CreatedAt: created, Metadata: metadata, Items: doc.Slice("items")}, nil
}

// UpdateConversation applies fn to the conversation under the store's lock and saves it.
func (s *Store) UpdateConversation(id, user string, fn func(*Conversation)) (*Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.GetConversation(id, user)
	if err != nil {
		return nil, err
	}
	fn(c)
	return c, s.saveConversation(c)
}

func (s *Store) DeleteConversation(id, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.GetConversation(id, user); err != nil {
		return err
	}
	return os.Remove(s.convPath(id))
}

func (s *Store) prune() {
	cutoff := time.Now().Add(-TTL)
	for _, dir := range []string{"responses", "conversations", filepath.Join("responses", "items")} {
		entries, err := os.ReadDir(filepath.Join(s.root, dir))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(s.root, dir, entry.Name()))
			}
		}
	}
}

// CloneItems is exported for callers that need an independent copy of stored items.
func CloneItems(items []any) []any { return cloneItems(items) }
