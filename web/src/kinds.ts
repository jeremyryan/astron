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
import gateway from "./assets/k8s/gateway.svg";
import generic from "./assets/k8s/generic.svg";
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
import role from "./assets/k8s/role.svg";
import cRole from "./assets/k8s/c-role.svg";
import rb from "./assets/k8s/rb.svg";
import crb from "./assets/k8s/crb.svg";
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
  Ingress: "#f1c40f",
  HTTPRoute: "#f1c40f",
  Gateway: "#16a085",
};

export function colorForKind(kind: string): string {
  return KIND_COLORS[kind] ?? "#7f8c8d";
}

// A distinct color per relationship (edge) type, used to color the arrows so
// the kind of relationship is visible at a glance. Unknown types fall back to a
// neutral grey.
export const RELATIONSHIP_COLORS: Record<string, string> = {
  OWNS: "#5b8def", // ownership (controller -> managed)
  SELECTS: "#e67e22", // Service / workload label selection
  MOUNTS: "#9b59b6", // ConfigMap / Secret / PVC consumed by a Pod
  BINDS: "#1abc9c", // PersistentVolume <-> PersistentVolumeClaim
  ROUTES: "#f1c40f", // Ingress / HTTPRoute -> Service
  RUNS: "#2ecc71", // ServiceAccount -> Pod (identity the pod runs as)
  GRANTS: "#e74c3c", // Role/ClusterRole -> the bindings that reference it
  DEFINES: "#e84393", // CRD -> its instances
  CUSTOM: "#16a3b8", // user-created link
};

export function colorForRelationship(type: string): string {
  return RELATIONSHIP_COLORS[type] ?? "#8a909c";
}

// Generic fallback icon shown for any Kubernetes kind that doesn't have a
// dedicated official icon.
export const GENERIC_ICON = generic;

// Official Kubernetes icon per kind. Unknown kinds fall back to GENERIC_ICON.
export const KIND_ICONS: Record<string, string> = {
  ConfigMap: cm,
  CustomResourceDefinition: crd,
  CronJob: cronjob,
  Deployment: deploy,
  DaemonSet: ds,
  Endpoints: ep,
  Gateway: gateway,
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
  Role: role,
  ClusterRole: cRole,
  RoleBinding: rb,
  ClusterRoleBinding: crb,
  StatefulSet: sts,
  Service: svc,
};

// genericIcon is the official Kubernetes badge shape/color with no inner glyph.
// Use it as a fallback for resource kinds that don't have a dedicated icon yet,
// so they still read as Kubernetes resources rather than a plain colored dot.
export const genericIcon = generic;

export function iconForKind(kind: string): string | undefined {
  return KIND_ICONS[kind];
}

// iconForKindOrGeneric returns the kind's dedicated icon, falling back to the
// generic Kubernetes badge so callers always have a badge to render.
export function iconForKindOrGeneric(kind: string): string {
  return KIND_ICONS[kind] ?? generic;
}
