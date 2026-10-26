# ADR 0001: Run Kubernetes as k3s on EC2

- **Status:** Accepted
- **Date:** 2026-10-14

## Context

Rule 5 requires Kubernetes microservices. Rule 2 caps infrastructure at USD 100/month.
Amazon EKS charges about USD 73/month for the control plane alone. Its usual companions,
a NAT Gateway (about USD 32/month) and an ALB (about USD 16/month), push it past the
budget before any workload runs.

## Options

1. **EKS (managed):** fully AWS-managed control plane. It costs about USD 73/month before
   any nodes.
2. **k3s on EC2:** a CNCF-certified Kubernetes distribution on our own Graviton instances.
   It runs the same manifests and Helm charts, and costs only the instances.
3. **ECS/Fargate:** cheaper and AWS-native, but it is not Kubernetes, so it breaks Rule 5.

## Decision

Run **k3s on EC2 Graviton (arm64)**, in this layout:

- **Server node:** 1 × `t4g.medium` on-demand. It runs the control plane and workloads,
  and is not spot so the control plane is not interrupted.
- **Agent nodes:** 0–2 × `t4g` spot instances, added when load needs them.
- **Datastore:** k3s uses embedded SQLite or etcd. It is snapshotted to S3 on a schedule.
- **Network:** nodes sit in public subnets with tight security groups, so there is no NAT
  Gateway. The Kubernetes API is not exposed to the internet; it is reached through SSM
  Session Manager port forwarding.
- **Ingress:** CloudFront and AWS WAF sit in front of Traefik, the ingress controller
  built into k3s. The origin accepts traffic from CloudFront only.
- **Pod access to AWS:** pods get scoped AWS credentials through IAM Roles Anywhere or a
  per-service role pattern. There are no node-wide broad permissions.

## Consequences

- **Benefits:** the cluster fits the budget, and the manifests and Helm charts stay
  portable to EKS.
- **Costs:**
  - We operate the control plane ourselves: upgrades, etcd snapshots and CIS hardening
    (k3s has a hardening guide that we follow).
  - A single server node has no control-plane high availability. Workloads keep running
    if the control plane briefly fails, but deployments pause.
- **Path to EKS:** move once revenue justifies about USD 73/month. Write the
  infrastructure so the cluster is a swappable CDK construct.
