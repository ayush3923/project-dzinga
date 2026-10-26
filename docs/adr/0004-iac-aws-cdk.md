# ADR 0004: Infrastructure as code with AWS CDK (TypeScript)

- **Status:** Accepted
- **Date:** 2026-10-14

## Context

Rule 2 requires every AWS resource to be defined as code. Rule 3 requires
Well-Architected practices, which includes repeatable, reviewable deployments.

## Decision

Use **AWS CDK v2 in TypeScript**, kept in `infra/`. Conventions:

- **Stacks:** a `network` stack, a `cluster` stack (k3s on EC2), a `data` stack, an `edge`
  stack (CloudFront, WAF and ACM in us-east-1), and an `observability` stack.
- **Security checks:** `cdk-nag` with the `AwsSolutions` rule pack runs in every
  `cdk synth`, and the PCI DSS pack is added where it applies. A warning that is
  suppressed needs a written reason in the code.
- **Tests:** stacks have Jest or Vitest snapshot tests and fine-grained assertion tests
  (Rule 8).
- **Tags:** every resource is tagged `project=dzinga`, `env=<env>`, `owner`,
  `cost-center`, so cost can be tracked per component.
- **Deploying:** only from CI, using GitHub OIDC to assume a deploy role, with no
  long-lived keys. A local `cdk deploy` is allowed only for the developer sandbox.

## Consequences

- **Benefits:** the same language as the frontend, and `cdk-nag` catches security and
  compliance problems before deployment.
- **Costs:** CDK generates CloudFormation, which has a 500-resource limit per stack. Keep
  stacks small. k3s itself is bootstrapped with EC2 user data and SSM documents defined in
  CDK; Helm charts are deployed by CI (or Argo CD later), not by CDK.
