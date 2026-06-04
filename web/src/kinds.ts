// Shared Kubernetes-kind presentation helpers used by both the graph view and
// the filter panel so colors stay consistent.

// A distinct color per common Kubernetes kind; unknown kinds fall back to grey.
export const KIND_COLORS: Record<string, string> = {
  Deployment: "#326ce5",
  StatefulSet: "#326ce5",
  DaemonSet: "#326ce5",
  ReplicaSet: "#5b8def",
  Pod: "#2ecc71",
  Service: "#e67e22",
  ConfigMap: "#9b59b6",
  Secret: "#c0392b",
};

export function colorForKind(kind: string): string {
  return KIND_COLORS[kind] ?? "#7f8c8d";
}
