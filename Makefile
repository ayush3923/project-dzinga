# Local development commands. Run `make help` for the list.
#
# DZ_INTERNAL_TOKEN authenticates ingest → core-api. It is generated once into
# .env.local (git-ignored) and never printed.

SHELL := /bin/bash
export PATH := /opt/homebrew/bin:$(PATH)
ENV_FILE := .env.local
TOKEN = $$(grep DZ_INTERNAL_TOKEN $(CURDIR)/$(ENV_FILE) | cut -d= -f2)
MS_CLIENT = $$(grep DZ_MS_CLIENT_ID $(CURDIR)/$(ENV_FILE) | cut -d= -f2)
ANTHROPIC_KEY = $$(grep '^ANTHROPIC_API_KEY=' $(CURDIR)/$(ENV_FILE) | cut -d= -f2-)

DB ?= $(CURDIR)/data/dzinga.db
# Optional: only scan mail on/after this date, e.g. SINCE=2026-08-14
SINCE_FLAG = $(if $(SINCE),--since $(SINCE),)

# core-api's port. 8091, not 8081: another local stack (CCI) uses 8080/8081.
CORE_PORT ?= 8091
CORE_URL = http://127.0.0.1:$(CORE_PORT)

.PHONY: help env core-api ingest-service quiz orgs orgs-import startoff startoff-import agent web ingest-gmail preview-gmail ingest-yahoo preview-yahoo ingest-outlook preview-outlook demo test test-go test-web vuln images k8s-validate

help:
	@echo "make core-api                        start the API on 127.0.0.1:$(CORE_PORT)"
	@echo "make ingest-service                  start the sync service on 127.0.0.1:8083 (Sync now, mailboxes)"
	@echo "make quiz                            start the quiz service on 127.0.0.1:8084 (daily quiz)"
	@echo "make orgs                            start the organizations service on 127.0.0.1:8085"
	@echo "make orgs-import FROM=<core db>      once: copy organizations from core-api's database (before its v14)"
	@echo "make startoff                        start the daily-jobs service on 127.0.0.1:8086 (07:00 run, company research)"
	@echo "make startoff-import FROM=<orgs db>  once: copy research from the organizations database (at its v2, before v3)"
	@echo "make agent                           start the Claude agent on 127.0.0.1:8082 (needs ANTHROPIC_API_KEY in .env.local)"
	@echo "make web                             start the web app on http://localhost:3000"
	@echo "make images REGISTRY=<ecr>           build and push all seven container images"
	@echo "make k8s-validate                    validate and lint the Kubernetes manifests"
	@echo "make preview-yahoo EMAIL=you@yahoo.com  dry run: list job emails found, send nothing"
	@echo "make ingest-yahoo EMAIL=you@yahoo.com   import job emails from Yahoo"
	@echo "make preview-gmail EMAIL=you@gmail.com   dry run for Gmail"
	@echo "make ingest-gmail EMAIL=you@gmail.com    import from Gmail"
	@echo "make preview-outlook EMAIL=you@live.in  dry run for a Microsoft account"
	@echo "make ingest-outlook EMAIL=you@live.in   import from a Microsoft account"
	@echo "make demo                            load the fictional demo mailbox (separate DB)"
	@echo "make test                            run all unit and integration tests"
	@echo "make vuln                            scan Go and npm dependencies for known vulnerabilities"

env:
	@test -f $(ENV_FILE) || { \
		echo "DZ_INTERNAL_TOKEN=$$(openssl rand -hex 32)" > $(ENV_FILE); \
		echo "DZ_DISPLAY_NAME=Ayush" >> $(ENV_FILE); \
		chmod 600 $(ENV_FILE); echo "Created $(ENV_FILE)"; }
	@mkdir -p data

core-api: env
	cd services/core-api && DZ_INTERNAL_TOKEN=$(TOKEN) DZ_CORE_ADDR=127.0.0.1:$(CORE_PORT) \
		DZ_DB_PATH=$(DB) go run .

# The sync service behind the web app's "Sync now" and Connected mailboxes panel.
ingest-service: env
	cd services/ingest && DZ_INTERNAL_TOKEN=$(TOKEN) DZ_MS_CLIENT_ID=$(MS_CLIENT) go run . --serve --core-url $(CORE_URL)

# The daily quiz (ADR 0008): its own database (data/quiz.db) and recordings (data/quiz-audio).
quiz: env
	cd services/quiz && DZ_INTERNAL_TOKEN=$(TOKEN) DZ_QUIZ_ADDR=127.0.0.1:8084 DZ_QUIZ_DB_PATH=$(CURDIR)/data/quiz.db go run .

# Organizations (ADR 0010): its own database (data/orgs.db); reads applications from core-api.
ORGS_URL = http://127.0.0.1:8085
orgs: env
	cd services/orgs && DZ_INTERNAL_TOKEN=$(TOKEN) DZ_ORGS_ADDR=127.0.0.1:8085 DZ_ORGS_DB_PATH=$(CURDIR)/data/orgs.db \
		DZ_CORE_URL=$(CORE_URL) go run .

# One-time move of organization data out of core-api, read-only from FROM (a backup works).
orgs-import: env
	@test -n "$(FROM)" || { echo "usage: make orgs-import FROM=data/backups/dzinga-….db"; exit 1; }
	cd services/orgs && DZ_ORGS_DB_PATH=$(CURDIR)/data/orgs.db go run . -import-core "$(abspath $(FROM))"

# Startoff (ADR 0011): the 07:00 daily run and company research, in its own database (data/startoff.db).
STARTOFF_URL = http://127.0.0.1:8086
startoff: env
	cd services/startoff && DZ_INTERNAL_TOKEN=$(TOKEN) DZ_STARTOFF_ADDR=127.0.0.1:8086 DZ_STARTOFF_DB_PATH=$(CURDIR)/data/startoff.db \
		DZ_ORGS_URL=$(ORGS_URL) go run .

# One-time move of research out of the organizations service, read-only from FROM (its
# database at v2, or a backup of it). Run before the organizations service's v3.
startoff-import: env
	@test -n "$(FROM)" || { echo "usage: make startoff-import FROM=data/backups/orgs-….db"; exit 1; }
	cd services/startoff && DZ_STARTOFF_DB_PATH=$(CURDIR)/data/startoff.db go run . -import-orgs "$(abspath $(FROM))"

agent: env
	@grep -q '^ANTHROPIC_API_KEY=.' $(ENV_FILE) || { echo "Add ANTHROPIC_API_KEY=... to .env.local first (it stays on your Mac)"; exit 1; }
	cd services/agent && DZ_INTERNAL_TOKEN=$(TOKEN) DZ_CORE_URL=$(CORE_URL) DZ_ORGS_URL=$(ORGS_URL) DZ_STARTOFF_URL=$(STARTOFF_URL) ANTHROPIC_API_KEY=$(ANTHROPIC_KEY) go run .

web: env
	cd apps/web && DZ_INTERNAL_TOKEN=$(TOKEN) CORE_API_URL=$(CORE_URL) ORGS_API_URL=$(ORGS_URL) STARTOFF_API_URL=$(STARTOFF_URL) DZ_DISPLAY_NAME=$$(grep DZ_DISPLAY_NAME ../../$(ENV_FILE) | cut -d= -f2) pnpm dev

preview-yahoo:
	@test -n "$(EMAIL)" || { echo "usage: make preview-yahoo EMAIL=you@yahoo.com"; exit 1; }
	cd services/ingest && go run . --user "$(EMAIL)" --dry-run $(SINCE_FLAG)

ingest-yahoo: env
	@test -n "$(EMAIL)" || { echo "usage: make ingest-yahoo EMAIL=you@yahoo.com"; exit 1; }
	cd services/ingest && DZ_INTERNAL_TOKEN=$(TOKEN) \
		go run . --user "$(EMAIL)" --core-url $(CORE_URL) $(SINCE_FLAG)

# Gmail over IMAP with an app password. "All Mail" holds inbox and archived mail.
GMAIL_IMAP = --imap-addr imap.gmail.com:993 --folders "[Gmail]/All Mail"

preview-gmail:
	@test -n "$(EMAIL)" || { echo "usage: make preview-gmail EMAIL=you@gmail.com"; exit 1; }
	cd services/ingest && go run . $(GMAIL_IMAP) --user "$(EMAIL)" --dry-run $(SINCE_FLAG)

ingest-gmail: env
	@test -n "$(EMAIL)" || { echo "usage: make ingest-gmail EMAIL=you@gmail.com"; exit 1; }
	cd services/ingest && DZ_INTERNAL_TOKEN=$(TOKEN) go run . $(GMAIL_IMAP) --user "$(EMAIL)" --core-url $(CORE_URL) $(SINCE_FLAG)

# Microsoft personal accounts (outlook.com, live.in, hotmail.com). Sign-in happens in
# the browser; needs DZ_MS_CLIENT_ID in .env.local (see docs/microsoft-setup.md).
preview-outlook:
	@test -n "$(EMAIL)" || { echo "usage: make preview-outlook EMAIL=you@live.in"; exit 1; }
	cd services/ingest && DZ_MS_CLIENT_ID=$(MS_CLIENT) \
		go run . --source graph --folders inbox,archive --user "$(EMAIL)" --dry-run $(SINCE_FLAG)

ingest-outlook: env
	@test -n "$(EMAIL)" || { echo "usage: make ingest-outlook EMAIL=you@live.in"; exit 1; }
	cd services/ingest && DZ_INTERNAL_TOKEN=$(TOKEN) DZ_MS_CLIENT_ID=$(MS_CLIENT) \
		go run . --source graph --folders inbox,archive --user "$(EMAIL)" --core-url $(CORE_URL) $(SINCE_FLAG)

# The demo uses its own database so fictional data never mixes with yours.
# Start core-api with `make core-api DB=$(CURDIR)/data/demo.db` first.
demo: env
	cd services/ingest && DZ_INTERNAL_TOKEN=$(TOKEN) \
		go run . --source dir --dir ./testdata/demo --since 2026-01-01

test: test-go test-web

test-go:
	cd services/core-api && go vet ./... && go test -race -cover ./...
	cd services/ingest && go vet ./... && go test -race -cover ./...
	cd services/agent && go vet ./... && go test -race -cover ./...
	cd services/quiz && go vet ./... && go test -race -cover ./...
	cd services/orgs && go vet ./... && go test -race -cover ./...
	cd services/startoff && go vet ./... && go test -race -cover ./...

test-web:
	cd apps/web && pnpm typecheck && pnpm lint && pnpm test

vuln:
	cd services/core-api && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	cd services/ingest && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	cd services/agent && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	cd services/quiz && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	cd services/orgs && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	cd services/startoff && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
	cd apps/web && pnpm audit --prod

# Container images (ADR 0006). Needs Docker with buildx and an ECR login:
#   make images REGISTRY=<account>.dkr.ecr.ap-south-1.amazonaws.com
PLATFORM ?= linux/arm64
IMAGE_TAG ?= $$(git rev-parse --short HEAD)
images:
	@test -n "$(REGISTRY)" || { echo "Set REGISTRY=<account>.dkr.ecr.ap-south-1.amazonaws.com"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "Commit first: image tags name a git commit"; exit 1; }
	for s in core-api agent ingest quiz orgs startoff; do \
		docker buildx build --platform $(PLATFORM) --provenance=true --sbom=true --push -t $(REGISTRY)/dzinga/$$s:$(IMAGE_TAG) services/$$s || exit 1; \
	done
	docker buildx build --platform $(PLATFORM) --provenance=true --sbom=true --push -t $(REGISTRY)/dzinga/web:$(IMAGE_TAG) apps/web

# Renders the manifests, checks them against the Kubernetes schemas and lints them. No cluster needed.
KUSTOMIZE = go run sigs.k8s.io/kustomize/kustomize/v5@v5.8.3
KUBECONFORM = go run github.com/yannh/kubeconform/cmd/kubeconform@v0.8.0
KUBE_LINTER = go run golang.stackrox.io/kube-linter/cmd/kube-linter@v0.8.3
k8s-validate:
	$(KUSTOMIZE) build deploy/k8s/overlays/prod > .k8s-rendered.yaml
	$(KUBECONFORM) -strict -summary -kubernetes-version 1.33.0 .k8s-rendered.yaml
	$(KUBE_LINTER) lint --config deploy/k8s/.kube-linter.yaml .k8s-rendered.yaml
	rm -f .k8s-rendered.yaml
