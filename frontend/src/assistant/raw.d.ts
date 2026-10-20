// Vite's `?raw` import: the file's text, bundled at build time.
declare module '*.md?raw' {
  const text: string
  export default text
}
