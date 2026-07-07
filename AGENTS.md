# Project Astron AGENTS.md

Project Astron is the code name for a project that will provide a way to visualize, explore and understand a Kubernetes cluster. It will accomplish this by implementing a Kubernetes operator that can be deployed within a cluster, that will watch for changes to the resources defined within the cluster, or within specified namespaces, and capture information about the resources defined and their connections as graph data within a Neo4J database. Nodes will be created for Kubernetes resources created within the cluster, and edges will represent the relationships between them. For example, each Deployment, StatefulSet and Daemon set will have an :OWNS edge to each Pod that was created based on its specification. Configuration resources like ConfigMaps and Secrets will have MOUNTS relationships with each Pod that mounts them within a container filesystem, and Services will have SELECTS relationships with the Pods that they expose traffic to. 

It will include the following components:

- A CRD named GraphProjection which will allow you to specify how the graph of resource relationships is projected into the Neo4J database
- A projection controller that watches for changes to GraphProjection CRD definitions and updates the configuration of projection instances
- A resource graph controller that uses the Kubernetes API to watch for changes in the cluster based on the configured projections defined by GraphProjection instances
- A web UI to display the captured set of resources and their relations, allowing administrators to get a useful view of the current state of the cluster
