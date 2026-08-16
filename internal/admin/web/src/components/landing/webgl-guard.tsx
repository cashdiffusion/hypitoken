import { Component, type ReactNode } from "react";

// R3F throws synchronously from render when a WebGL context can't be created
// (GPU-blocklisted drivers, remote desktops, hardened browsers). Without a
// local boundary that escalates to the root AppErrorBoundary and a purely
// decorative backdrop takes the whole page down. Guard = cheap capability
// probe up front + boundary for anything the probe can't predict; either way
// the decoration just disappears.
let supported: boolean | null = null;
function webglSupported(): boolean {
  if (supported !== null) return supported;
  try {
    const c = document.createElement("canvas");
    supported = Boolean(c.getContext("webgl2") ?? c.getContext("webgl"));
  } catch {
    supported = false;
  }
  return supported;
}

interface Props {
  children: ReactNode;
}

interface State {
  failed: boolean;
}

export class WebGLGuard extends Component<Props, State> {
  state: State = { failed: false };

  static getDerivedStateFromError(): State {
    return { failed: true };
  }

  render() {
    if (this.state.failed || !webglSupported()) return null;
    return this.props.children;
  }
}
