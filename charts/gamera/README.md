# Gamera Helm Chart

Deploys the **Project Gamera** operator — which projects Kubernetes cluster
resources and their relationships into a Neo4J graph — together with the
`GraphProjection` CRD, RBAC, and (optionally) a bundled Neo4J database.

## TL;DR

```sh
# Fetch the Neo4J dependency once.
helm dependency build charts/gamera

# Install with a bundled Neo4J (development).
helm install gamera charts/gamera \
  --namespace gamera-system --create-namespace \
  --set neo4j.neo4j.password='a-strong-password'
```

Open the UI:

```sh
kubectl -n gamera-system port-forward svc/gamera-api 8082:8082
# browse http://localhost:8082
```

## Neo4J: bundled vs. external

The chart can either deploy a Neo4J instance for you (the official
[`neo4j`](https://helm.neo4j.com/neo4j) chart, included as a dependency) or
connect to an existing one.

### Bundled (default)

`neo4j.enabled=true` deploys Neo4J as a subchart. The operator's connection URI
is auto-derived from the in-cluster service, and the credentials Secret reuses
the bundled password.

```sh
helm install gamera charts/gamera -n gamera-system --create-namespace \
  --set neo4j.neo4j.password='a-strong-password'
```

### External / existing instance

Set `neo4j.enabled=false` and point the chart at your database. Provide
credentials either via an existing Secret or inline values.

```sh
helm install gamera charts/gamera -n gamera-system --create-namespace \
  --set neo4j.enabled=false \
  --set connection.uri='neo4j://neo4j.data.svc.cluster.local:7687' \
  --set connection.existingSecret='neo4j-credentials'
```

The existing Secret must contain the keys named by `connection.usernameKey`
(default `username`) and `connection.passwordKey` (default `password`).

## Key values

| Key | Default | Description |
| --- | --- | --- |
| `image.repository` / `image.tag` | `ghcr.io/project-gamera/gamera` / appVersion | Operator image. |
| `replicaCount` | `1` | Operator replicas (leader-elected). |
| `crds.install` | `true` | Install the GraphProjection CRD (kept on uninstall). |
| `rbac.create` | `true` | Create ClusterRole/Role and bindings. |
| `controller.leaderElection` | `true` | Enable leader election. |
| `controller.metrics.enabled` / `.secure` | `true` / `true` | Metrics endpoint and auth. |
| `controller.api.enabled` | `true` | Read API + embedded web UI. |
| `service.api.port` | `8082` | UI/API service port. |
| `connection.uri` | `""` | Bolt URI; auto-derived for bundled Neo4J. |
| `connection.database` | `neo4j` | Target database. |
| `connection.existingSecret` | `""` | Use an existing credentials Secret. |
| `connection.username` / `connection.password` | `neo4j` / `""` | Credentials for the chart-managed Secret. |
| `defaultProjection.enabled` | `true` | Create a default `GraphProjection`. |
| `neo4j.enabled` | `true` | Deploy the bundled Neo4J subchart. |
| `neo4j.neo4j.password` | `change-me-please` | Bundled Neo4J password — **change it**. |

See [`values.yaml`](./values.yaml) for the full set, including resources,
scheduling, security contexts, and the default projection's scope/relationships.

## Uninstall

```sh
helm uninstall gamera -n gamera-system
```

The CRD is annotated with `helm.sh/resource-policy: keep`, so it (and any
`GraphProjection` resources) survive uninstall. Remove it manually if desired:

```sh
kubectl delete crd graphprojections.gamera.gamera.io
```
