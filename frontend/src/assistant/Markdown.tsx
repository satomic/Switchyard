import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { isAssistantPath, type AssistantRoute } from './routes'

/** The subset of Markdown a chat answer uses: paragraphs, headings, bullet and numbered lists,
 *  fenced code, inline code, bold and links.
 *
 *  Built as React elements, never as HTML, so nothing the model writes can inject markup. A link
 *  to a console page becomes an in-app <Link> -- a click navigates without reloading and keeps the
 *  conversation -- but only when it names a real page; a path the model made up stays plain text
 *  rather than leading to "not found". External http(s) links open in a new tab. */
export default function Markdown({
  text,
  routes,
  onNavigate,
}: {
  text: string
  routes: AssistantRoute[]
  onNavigate?: () => void
}) {
  const blocks: ReactNode[] = []
  const lines = text.replace(/\r\n/g, '\n').split('\n')
  let i = 0
  let key = 0
  const inline = (s: string) => renderInline(s, routes, onNavigate)

  while (i < lines.length) {
    const line = lines[i]
    if (line.startsWith('```')) {
      const code: string[] = []
      i++
      while (i < lines.length && !lines[i].startsWith('```')) code.push(lines[i++])
      i++
      blocks.push(<pre key={key++} className="assistant-code">{code.join('\n')}</pre>)
      continue
    }
    const heading = /^#{1,6}\s+(.*)$/.exec(line)
    if (heading) {
      blocks.push(<p key={key++} className="assistant-heading">{inline(heading[1])}</p>)
      i++
      continue
    }
    if (/^\s*[-*]\s+/.test(line) || /^\s*\d+[.)]\s+/.test(line)) {
      const ordered = /^\s*\d+[.)]\s+/.test(line)
      const items: ReactNode[] = []
      while (i < lines.length && (ordered ? /^\s*\d+[.)]\s+/ : /^\s*[-*]\s+/).test(lines[i])) {
        items.push(<li key={items.length}>{inline(lines[i].replace(/^\s*(?:[-*]|\d+[.)])\s+/, ''))}</li>)
        i++
      }
      blocks.push(ordered ? <ol key={key++}>{items}</ol> : <ul key={key++}>{items}</ul>)
      continue
    }
    if (!line.trim()) {
      i++
      continue
    }
    const para: string[] = []
    while (i < lines.length && lines[i].trim() && !/^(```|#{1,6}\s|\s*[-*]\s|\s*\d+[.)]\s)/.test(lines[i])) {
      para.push(lines[i++])
    }
    blocks.push(<p key={key++}>{inline(para.join(' '))}</p>)
  }
  return <div className="assistant-md">{blocks}</div>
}

function renderInline(text: string, routes: AssistantRoute[], onNavigate?: () => void): ReactNode[] {
  const out: ReactNode[] = []
  // Code first, so a bracket or star inside backticks is not read as a link or bold.
  const pattern = /`([^`]+)`|\*\*([^*]+)\*\*|\[([^\]]+)\]\(([^)\s]+)\)/g
  let last = 0
  let match: RegExpExecArray | null
  let key = 0
  while ((match = pattern.exec(text))) {
    if (match.index > last) out.push(...linkBarePaths(text.slice(last, match.index), routes, onNavigate, () => key++))
    if (match[1] !== undefined) {
      // A page path in backticks -- the knowledge base writes them that way, and a model copies
      // the habit -- is still a page: linked, under the page's name.
      const route = routeFor(match[1], routes)
      out.push(
        route ? (
          <Link key={key++} to={match[1]} className="assistant-link" onClick={onNavigate}>{route.label}</Link>
        ) : (
          <code key={key++}>{match[1]}</code>
        ),
      )
    } else if (match[2] !== undefined) {
      out.push(<strong key={key++}>{match[2]}</strong>)
    } else {
      const [label, target] = [match[3], match[4]]
      if (target.startsWith('/') && isAssistantPath(target, routes)) {
        out.push(
          <Link key={key++} to={target} className="assistant-link" onClick={onNavigate}>
            {label}
          </Link>,
        )
      } else if (/^https?:\/\//.test(target)) {
        out.push(
          <a key={key++} href={target} target="_blank" rel="noreferrer">
            {label}
          </a>,
        )
      } else {
        out.push(label)
      }
    }
    last = match.index + match[0].length
  }
  if (last < text.length) out.push(...linkBarePaths(text.slice(last), routes, onNavigate, () => key++))
  return out
}

function routeFor(path: string, routes: AssistantRoute[]): AssistantRoute | undefined {
  return routes.find((r) => r.path === path)
}

/** A bare page path in running text -- "(/models)", "【可用模型】（/models）" -- becomes a link
 *  too, when it names a page this reader can open. Anything else that looks like a path (an API
 *  endpoint such as /v1/chat/completions, a page they cannot open) stays text. */
function linkBarePaths(
  text: string,
  routes: AssistantRoute[],
  onNavigate: (() => void) | undefined,
  nextKey: () => number,
): ReactNode[] {
  const out: ReactNode[] = []
  const pattern = /(^|[^\w/.])(\/[a-z][a-z/]*[a-z])(?![\w/])/g
  let last = 0
  let match: RegExpExecArray | null
  while ((match = pattern.exec(text))) {
    const route = routeFor(match[2], routes)
    if (!route) continue
    const start = match.index + match[1].length
    if (start > last) out.push(text.slice(last, start))
    out.push(
      <Link key={`p${nextKey()}`} to={route.path} className="assistant-link" onClick={onNavigate}>
        {route.label}
      </Link>,
    )
    last = start + match[2].length
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}
