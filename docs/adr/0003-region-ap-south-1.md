# ADR 0003: Primary AWS region is Mumbai (ap-south-1)

- **Status:** Accepted
- **Date:** 2026-10-14

## Context

The first customers are expected to be in India. Latency matters, and keeping data in
India helps with local data-protection expectations (the DPDP Act 2023).

## Decision

Deploy all workloads and data to **ap-south-1 (Mumbai)** in a single region, using
2 Availability Zones for subnets. Compute starts in one AZ to save cost (see ADR 0001).

Some resources are global or must live in us-east-1:

- the CloudFront distribution,
- the WAF web ACL attached to CloudFront,
- the ACM certificate for CloudFront.

The CDK app deploys these in a separate small `us-east-1` stack.

## Consequences

- **Benefits:** low latency for Indian users, and customer data stays in India.
- **Costs:**
  - Some instance types and services launch later in ap-south-1. Check availability
    before adopting a new service.
  - Prices in ap-south-1 differ slightly from us-east-1. Cost estimates in
    `docs/cost.md` use Mumbai prices.
