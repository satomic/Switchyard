import { Component, type ReactNode } from 'react'

/** Contains a render-time failure to the component that failed.
 *
 *  Without a boundary React unmounts the whole tree on an uncaught render error, so a fault in an
 *  optional widget -- the AI assistant threw on plain-HTTP deployments in 2.6.0 -- left the entire
 *  console as a blank page. Wrapped around such widgets, the failure removes only the widget and
 *  is logged for diagnosis. */
export default class ErrorBoundary extends Component<{ name: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  componentDidCatch(error: unknown) {
    console.error(`${this.props.name} failed and was hidden:`, error)
  }

  render() {
    return this.state.failed ? null : this.props.children
  }
}
