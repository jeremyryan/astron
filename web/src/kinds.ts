// Shared Kubernetes-kind presentation helpers used by both the graph view and
// the filter panel so colors and icons stay consistent.

// Official Kubernetes resource icons (from github.com/kubernetes/community,
// icons/svg, unlabeled variants). Imported as URLs so only the ones used are
// emitted as assets.
import cm from "./assets/k8s/cm.svg";
import crd from "./assets/k8s/crd.svg";
import cronjob from "./assets/k8s/cronjob.svg";
import deploy from "./assets/k8s/deploy.svg";
import ds from "./assets/k8s/ds.svg";
import ep from "./assets/k8s/ep.svg";
import hpa from "./assets/k8s/hpa.svg";
import ing from "./assets/k8s/ing.svg";
import job from "./assets/k8s/job.svg";
import netpol from "./assets/k8s/netpol.svg";
import node from "./assets/k8s/node.svg";
import ns from "./assets/k8s/ns.svg";
import pod from "./assets/k8s/pod.svg";
import pv from "./assets/k8s/pv.svg";
import pvc from "./assets/k8s/pvc.svg";
import rs from "./assets/k8s/rs.svg";
import sa from "./assets/k8s/sa.svg";
import secret from "./assets/k8s/secret.svg";
import sts from "./assets/k8s/sts.svg";
import svc from "./assets/k8s/svc.svg";

// A distinct color per common Kubernetes kind; unknown kinds fall back to grey.
// Used as a fallback glyph (and accents) where no icon is available.
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

// Official Kubernetes icon per kind. Unknown kinds have no icon and fall back to
// the colored glyph.
export const KIND_ICONS: Record<string, string> = {
  ConfigMap: cm,
  CustomResourceDefinition: crd,
  CronJob: cronjob,
  Deployment: deploy,
  DaemonSet: ds,
  Endpoints: ep,
  HorizontalPodAutoscaler: hpa,
  Ingress: ing,
  Job: job,
  NetworkPolicy: netpol,
  Node: node,
  Namespace: ns,
  Pod: pod,
  PersistentVolume: pv,
  PersistentVolumeClaim: pvc,
  ReplicaSet: rs,
  ServiceAccount: sa,
  Secret: secret,
  StatefulSet: sts,
  Service: svc,
};

export function iconForKind(kind: string): string | undefined {
  return KIND_ICONS[kind];
}
