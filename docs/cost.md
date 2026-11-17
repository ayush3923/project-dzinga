# Monthly cost tracker

**Budget:** USD 100/month (Rule 2). AWS Budgets alerts fire at USD 50, 80 and 100.

The figures below are **estimates for ap-south-1 (Mumbai)** from list prices. Check them
in the AWS Pricing Calculator before deploying. Update this table whenever a component
is added or removed.

| Component | Choice | Est. USD/month | Notes |
|---|---|---:|---|
| k3s server node | 1 × t4g.medium on-demand | ~20 | Control plane and workloads |
| k3s agent nodes | 0–2 × t4g.small spot | 0–8 | Added only when needed |
| Block storage | gp3, about 40 GB total | ~4 | Node root volumes |
| Public IPv4 | 1 address | ~4 | AWS charges for every public IPv4 address |
| Database | RDS PostgreSQL db.t4g.micro, single-AZ, 20 GB | ~18 | *Open decision:* RDS, or Postgres inside the cluster |
| CDN and edge | CloudFront | ~0–2 | Inside the free tier at launch |
| WAF | 1 web ACL and managed rule groups | ~10 | Needed for PCI DSS 6.4.2 |
| DNS | Route 53 hosted zone | ~1 | |
| Secrets | Secrets Manager, about 5 secrets | ~2 | |
| Container registry | ECR | ~1 | Lifecycle policy keeps only the last N images |
| Logs and metrics | CloudWatch | ~5–10 | Short retention; audit logs go to S3 |
| Backups and audit logs | S3 Standard-IA or Glacier IR | ~1 | |
| Volume snapshots | EBS snapshots via Data Lifecycle Manager, about 5 GB changed | ~0.5 | Backup for the core-api database (ADR 0006) |
| Claude work queue (ADR 0007) | Scheduled Claude task on the owner's subscription | 0 | No API calls |
| Quiz service (ADR 0008) | 1 small pod (32–128 MiB) on the existing server node, 2 GiB volume | ~0.2 | gp3 storage for the volume; no new instance |
| **Total** | | **~67–82** | Leaves about USD 20 headroom |

## Deliberately avoided

| Component | Cost | How we avoid it |
|---|---:|---|
| EKS control plane | ~73/month | k3s on EC2 (ADR 0001) |
| NAT Gateway | ~32/month plus data | Public subnets and VPC gateway endpoints |
| Application Load Balancer | ~16/month or more | CloudFront in front of Traefik on the node |
| Amazon Managed Grafana | 9 per editor per month | Self-hosted Grafana, or CloudWatch dashboards (to be decided) |
