# Keygate AI Collaboration Entry

This file keeps only project scope, non-negotiable safety guardrails, and navigation. Read the [AI Collaboration Guide](docs/AI协作指南.md) by task; read deployment details in the sibling control-plane project's [single-VPS deployment guide](../digital-warehousing-control-plane/deploy/keygate/README.md), and read the source-release process in [WMS Self-Maintained Keygate Release](docs/dw-release.md).

## Baseline Settings

- This repository contains self-maintained Keygate source and release constraints. WMS business code, vendor deployment configuration, and the custom licensing/control plane are outside this repository.
- Preserve AGPL v3, Section 7(b), NOTICE, the original Go module path, and the “Powered by Keygate” attribution in every interface; do not help bypass attribution requirements.
- Build Keygate only from reviewed immutable tags. Pin both the image version and manifest digest; never use `latest`.
- Never commit `.env` files, API keys, license or signing keys, database or object-storage data, backup identities, or generated WMS artifacts.
- SafeLine owns public TCP ports 80/443. Allow the Keygate administration plane only through WireGuard, keep the MinIO console private, and do not add compatibility services for the retired control plane.

## Tool Entry Points

- For unfamiliar modules, cross-file call chains, dependency tracing, architecture questions, or blast-radius analysis, use CodeGraph `codegraph_explore` first, then read only the necessary code.
- Use RTK only for noisy, summary-oriented read-only commands such as `rtk git status`, `rtk go test ./...`, `rtk npm test`, and `rtk docker compose ps`.
- Use native commands for exact diffs, raw configuration or logs, `rg`, security audits, installation, commits, pushes, deployments, and database or backup operations.
- The complete CodeGraph and RTK policy, fallback behavior, and on-demand reading flow are in [AI Collaboration Guide: Tools and Context](docs/AI协作指南.md#1-工具与上下文).

## Task Routing

| Task | Read as needed |
| --- | --- |
| Keygate source, licensing, or upstream sync | [AI Collaboration Guide: Scope and Licensing](docs/AI协作指南.md#2-范围与许可证) |
| SafeLine, networking, Compose, or images | [AI Collaboration Guide: Deployment and Networking](docs/AI协作指南.md#3-部署与网络) and [Control-Plane Single-VPS Deployment Guide](../digital-warehousing-control-plane/deploy/keygate/README.md) |
| Database, MinIO, backups, or recovery | [AI Collaboration Guide: Data and Backups](docs/AI协作指南.md#4-数据与备份) |
| Tests, audits, or script checks | [AI Collaboration Guide: Validation and Delivery](docs/AI协作指南.md#5-验证与交付) |
| WMS self-maintained tags | [docs/dw-release.md](docs/dw-release.md) |

## Every Task and Delivery

1. Run `git status --short` first, then locate the target with `rg -n`; read only matching areas and direct dependencies.
2. Preserve existing user changes and avoid unrelated refactors; update related documentation and tests when user-visible behavior, deployment boundaries, or backup flows change.
3. At minimum, run the tests, audits, Compose configuration checks, Shell syntax checks, and `git diff --check` required by the project guide before delivery.
4. The final summary should report only the result, key impact, executed validation, and real limitations.
