# Astron

Astron is a Kubernetes operator that visualizes, explores, and helps you
understand a cluster. It watches cluster resources and projects them — and the
relationships between them — into a [Neo4J](https://neo4j.com/) graph, then
serves a web UI to explore that graph.

- **Nodes** are Kubernetes resources (Deployments, StatefulSets, DaemonSets,
  Pods, Services, ConfigMaps, Secrets, ...).
- **Edges** are their relationships, e.g. `OWNS` (workload → Pod via owner
  references), `SELECTS` (Service → Pod via label selectors), and `MOUNTS`
  (ConfigMap/Secret → Pod via volumes/env).

## Components

- **`GraphProjection` CRD** — declares how the cluster graph is projected into
  Neo4J: the scope of resources to capture and the relationship rules. The
  Neo4J connection itself is configured once on the controller (flags,
  `ASTRON_NEO4J_*` environment variables, or a mounted config file) and shared
  by all projections.
- **GraphProjection controller** — reconciles `GraphProjection` resources and
  manages a per-projection *projector*.
- **Resource graph projector** — dynamic informers that watch the in-scope
  resources, materialize them as nodes, and apply the relationship engine to
  materialize edges (`internal/projector`, `internal/relationship`).
- **Read API + web UI** — a read-only HTTP API over the graph and an embedded
  React/Cytoscape single-page app (`internal/api`, `web/`).

The operator binary serves the controller, the API, and the UI together.

## Local Development (dev loop)

The fastest way to iterate is [Skaffold](https://skaffold.dev/), wired up in
[`skaffold.yaml`](./skaffold.yaml). It builds the operator image, pushes it to a
local registry, deploys the Helm chart (including a bundled Neo4J), and watches
for changes.

### Prerequisites

- Go v1.24+, Docker (with BuildKit — default in modern Docker), `kubectl`, `helm` v3.
- [`skaffold`](https://skaffold.dev/docs/install/) v2.
- A local Kubernetes cluster (e.g. k3s/kind) you can reach with `kubectl`.
- A local image registry at **`localhost:5000`** that the cluster can pull from.
  On single-node clusters this works out of the box because Docker and
  containerd treat `localhost:` registries as insecure (HTTP) by default.

### Start the dev loop

```sh
make dev
```

This runs `skaffold dev`, which on every change to Go sources, the web UI, or
the chart will:

1. build the operator image (web UI + Go binary) via the multi-stage
   [`Dockerfile`](./Dockerfile),
2. push it to `localhost:5000/astron` with a content-based (digest) tag,
3. `helm upgrade` the `astron` release into the `astron` namespace, injecting the
   freshly built, digest-pinned image, and
4. **port-forward the UI** to <http://localhost:8082>.

The `Dockerfile` uses BuildKit `--mount=type=cache` for the npm, Go module, and
Go build caches, so incremental rebuilds take a few seconds rather than ~40.

Press Ctrl-C to stop; Skaffold tears down what it deployed.

### Other Skaffold targets

```sh
make helm-deps        # add the Neo4J helm repo + build chart dependencies (run once / on a fresh checkout)
make skaffold-run     # one-shot: build -> push -> deploy (does NOT keep a port-forward; see below)
make skaffold-render  # render the manifests Skaffold would deploy (verifies image substitution)
make skaffold-delete  # tear down the Skaffold-deployed release
```

### Accessing the UI

The `portForward` in `skaffold.yaml` is only active for commands that keep
running and watch — **`skaffold dev` and `skaffold debug`**. It is *not* set up
by `skaffold run`/`make skaffold-run`, which deploy and then exit.

- While `make dev` is running: open <http://localhost:8082>.
- After a one-shot deploy (`make skaffold-run`), forward the service manually:

  ```sh
  kubectl -n astron port-forward svc/astron-api 8082:8082
  # then browse http://localhost:8082
  ```

### Inspecting the projected graph

```sh
kubectl get graphprojection -A                 # status, node/edge counts
kubectl -n astron get graphprojection default -o yaml
```

The read API is also available (under the same port-forward):

```sh
curl http://localhost:8082/api/projections
curl http://localhost:8082/api/projections/astron/default/graph
```

### GraphRAG (semantic search, Q&A, text-to-Cypher, MCP)

Astron can expose the projected graph to LLM/agent applications: semantic
search, graph-neighborhood retrieval, grounded question answering, and guarded
text-to-Cypher — over HTTP and via the [Model Context Protocol](https://modelcontextprotocol.io/).
It is opt-in per `GraphProjection`.

- **[UI User Guide](./docs/ui-guide.md)** — how to explore the cluster graph in
  the web UI: filters, views, selection, custom links, layout, and shortcuts.
- **[GraphRAG User Guide](./docs/graphrag-guide.md)** — enable it, configure a
  provider, call the API, and wire up the `astron mcp-server`.
- **[GraphRAG Design](./docs/graphrag.md)** — architecture and rationale.

## Getting Started

### Prerequisites
- go version v1.24.6+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/astron:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/astron:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/astron:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/astron/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

The operator ships a Helm chart at [`charts/astron`](./charts/astron) that
installs the CRD, the controller, RBAC, and — optionally — a bundled Neo4J (or
connects to an existing one). See [`charts/astron/README.md`](./charts/astron/README.md)
for the full set of values.

```sh
helm dependency build charts/astron   # fetch the Neo4J subchart (once)
helm install astron charts/astron \
  --namespace astron --create-namespace \
  --set neo4j.neo4j.password='a-strong-password'
```

To connect to an existing Neo4J instead of the bundled one, set
`neo4j.enabled=false` and provide `connection.uri` plus credentials
(`connection.existingSecret` or `connection.username`/`connection.password`).

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

### Third-party assets

The web UI bundles the official Kubernetes resource icons from
[`kubernetes/community`](https://github.com/kubernetes/community/tree/master/icons)
(Apache-2.0) to represent resource kinds. See [`NOTICE`](./NOTICE) and
[`web/src/assets/k8s/ATTRIBUTION.md`](./web/src/assets/k8s/ATTRIBUTION.md) for
details. "Kubernetes" and the Kubernetes logo are trademarks of The Linux
Foundation, used here descriptively and subject to the
[CNCF/LF trademark guidelines](https://www.linuxfoundation.org/legal/trademark-usage).

