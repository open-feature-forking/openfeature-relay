# Docs rewrite plan: present OpenFeature Relay first, flagd as its reference distribution

**Status: decisions taken by the project lead (section 0).** Base: `main` @ e68610d. The README draft is in `README.md` on this branch.

Goal: a visitor who lands on the repository or the site learns in one screen what OpenFeature Relay is, that it is a proof of concept, what works today, and that flagd-full is today's flagd. Then the repository can be made public without leaking personal workflow details or implying it is an official OpenFeature project.

Not in scope: changing the spec's content, code, or the flagd docs' substance. The flagd pages are moved, not rewritten.

## 0. Decisions

| # | Question | Decision |
|---|----------|----------|
| D1 | Public name | Keep "OpenFeature Relay", with a disclaimer on the README and the site home: an independent proof of concept, not an official OpenFeature or CNCF project. The project lead gives the OpenFeature TC a heads-up before the repository goes public (P8e). |
| D2 | Spec on the site | Yes, under "Design". |
| D3 | `.claude/` and `CLAUDE.md` | Public; `CLAUDE.md` trimmed of local paths and personal details, gates also documented generically in `CONTRIBUTING.md`. |
| D4 | Research note | Published under "Design" after the cleanup (P6). |
| D5 | Site hosting | GitHub Pages from this repository; `netlify.toml` deleted. |
| D6 | flagd docs | All kept, paths unchanged; ADRs added to the nav under flagd. |
| D7 | Badges | Upstream badges and contributors image dropped; upstream contributors credited by link. |
| D8 | The project lead's name | Removed everywhere; "the project lead" instead. |
| D9 | `renovate.json` | Removed. |

## 1. Inventory

### 1.1 Top-level files

| File | Today | Action |
|------|-------|--------|
| `README.md` | flagd's README (logo, badges pointing at `open-feature/flagd`, quick start with `ghcr.io/open-feature/flagd`, contrib.rocks for upstream) | Replace with the Relay README (draft on this branch). flagd's quick start moves to the site's flagd section (it is already `docs/quick-start.md`). |
| `CLAUDE.md` | Working agreement, gates with a local Windows path, the sign-off address, the project lead's name | Trim (P1–P3). Keep the loop, phases, PoC order and conventions. |
| `CONTRIBUTING.md` | flagd's: code of conduct link to open-feature/.github, `make test`, manual testing with curl/grpcurl, DCO, conventional PR titles, markdownlint | Rewrite the intro (what contributions fit a PoC; open an issue first), keep Development, Manual testing, DCO and lint sections. Add the gate table from `CLAUDE.md` in generic form (Go toolchain pin, `GOWORK` in worktrees, `GOPATH` for lint). Remove the Brigade reference in the DCO text (copy-paste artefact from upstream). |
| `CODEOWNERS` | `@open-feature/flagd-maintainers @open-feature/maintainers`, "Managed by Peribolos" | Replace with this repo's owner(s); the upstream teams do not exist in `open-feature-forking`. |
| `CHANGELOG.md` | flagd's release history | Keep (history); add a one-line note at the top that it is upstream flagd's changelog up to the branch point. |
| `.release-please-manifest.json`, `release-please-config.json` | flagd's release config | Keep; the workflow is disabled (P9). |
| `netlify.toml` | flagd.dev's Netlify build | Delete if D5 is accepted. |
| `sonar-project.properties` | flagd's SonarCloud project | Delete, or point at nothing; it names flagd's project key. |
| `renovate.json` | flagd's Renovate config | Delete (D9). |
| `benchmark.txt`, `images/` (`flagD.png`, `flagd-proxy.png`, `loadTestResults.png`) | flagd artefacts | Keep; `images/flagD.png` is used by flagd docs. Add a Relay logo only if one exists; none is proposed. |
| `.github/ISSUE_TEMPLATE/*` | flagd's bug/feature/docs templates | Reword "flagd" → "OpenFeature Relay (or flagd-full)"; keep the shape. |
| `.github/workflows/` | build, lint-pr, markdown-checks, release-please (disabled), dev-benches-page (disabled) | See P9–P11. |
| `Dockerfile`, `flagd/build.Dockerfile`, `systemd/`, `config/`, `samples/`, `playground-app/` | flagd's | Unchanged. |

### 1.2 `mkdocs.yml`

- `site_name: flagd`, `site_description: A feature flag daemon with a Unix philosophy`, `repo_url: https://github.com/open-feature/flagd`. `edit_uri` therefore sends "edit this page" to upstream. → Rename and point at this repo.
- `exclude_docs` lists the spec but **not** `docs/research/`; `mkdocs build` publishes `research/cross-vendor-evaluation.md` today as an orphan page. It also lists `test_cases/`, `processes/` and `entity_model.md`, which do not exist yet (harmless).
- `docs/architecture-decisions/` is not in `nav` but is built (orphan pages).
- Redirects map old flagd.dev paths; keep them (harmless, and they make relative links from upstream docs resolve).

### 1.3 `docs/` pages

Legend: **flagd** = stays as flagd distribution docs (moved under the flagd section, content unchanged); **Relay** = becomes or feeds a generic Relay page; **spec** = AI Unified Process artefact; **reword** = small edits needed.

| Path | Covers today | Class | Notes |
|------|--------------|-------|-------|
| `index.md` | What flagd is, how to deploy it | flagd + Relay | Becomes `flagd/index.md`; new Relay `index.md` replaces it as the home page. |
| `quick-start.md` | Run flagd, evaluate with curl | flagd | Under flagd. The Relay "Try it" page links here, plus "build from source" instructions. |
| `installation.md` | Binaries, Docker, Homebrew, systemd from `open-feature/flagd` releases | flagd, reword | Add an admonition: these install upstream flagd; this repository publishes no releases yet. |
| `playground/` | Generated in-browser playground | flagd | Generated by `make generate-docs`; keep. |
| `concepts/feature-flagging.md` | Flags, evaluation context | flagd → also Relay | Generic enough to link from Relay concepts. |
| `concepts/syncs.md` | Sync sources (file, HTTP, gRPC, K8s, blob) | flagd + Relay | The Relay "Sync providers" concept links here as the flagd-full set. |
| `concepts/selectors.md`, `concepts/metadata.md` | Selectors, flag set metadata | flagd | Selector is also a glossary term; link from Relay concepts. |
| `architecture.md` | RPC vs in-process, flagd-proxy | flagd | Relay gets its own architecture page (component diagram). |
| `guides/*` | Flag sets migration, A/B experiments | flagd | Unchanged. |
| `providers/*` | Generated provider pages | flagd | Generated; unchanged. |
| `reference/*` | CLI, sync config, selector syntax, gRPC sync, OFREP, flag definitions, custom ops, schema, monitoring, FIPS, specifications, operator, naming, cheat sheet | flagd | All under "flagd → Reference". `fips-140-3.md` is linked from the glossary (FIPS Build); update that link when moving. |
| `faq.md`, `troubleshooting.md` | flagd | flagd | Under flagd; add one Relay FAQ page later (not now). |
| `architecture-decisions/*` | flagd ADRs | flagd | Add to nav under "flagd → Architecture decisions". `multiple-flag-set-support(rejected).md` has parentheses in the file name; leave it. |
| `assets/`, `images/`, `schema/v0/` | Logo, CSS, sample flags, published JSON schemas | flagd | `schema/v0/*` URLs are referenced by `$schema` in flag files as `https://flagd.dev/schema/v0/...`; do not move them and keep pointing users at flagd.dev for schema URLs. |
| `vision.md`, `requirements.md`, `glossary.md`, `use_cases.puml`, `use_cases/` | Spec | spec | On the site under "Design" (D2). `requirements.md` is titled "Requirements: flagd Builder" → retitle "Requirements: OpenFeature Relay" (the glossary already avoids "flagd-builder"). |
| `research/cross-vendor-evaluation.md` | Study of flagd, Flagsmith, GOFF against the component model | spec, reword | Published accidentally today (not excluded). Clean up (P6), then put under "Design → Research" (D4). |

Nothing in `docs/` is obsolete outright. Two things are stale: the flagd home page's claim to be the whole project, and `installation.md` pointing at releases this repository does not make.

## 2. Information architecture

### 2.1 Site layout (target)

Moving flagd pages under `docs/flagd/` breaks every relative link between them and from the generated pages. Proposal: **keep the files where they are and change only the nav**, plus the new pages below. The URL structure stays flagd.dev-compatible, which also keeps the `redirects` plugin and the generators (`make generate-docs`, `generate-proto-docs`) working unchanged. The cost is that URLs do not show the hierarchy; the nav does.

```text
Home                                   index.md            (new: Relay)
Why OpenFeature Relay                  relay/why.md        (new, from vision.md "Problem" and "Vision")
Concepts                               relay/concepts.md   (new: components, component types, distributions,
                                                            builder, manifest, configuration formats, evaluators,
                                                            store, event bus; glossary terms, linked)
Architecture                           relay/architecture.md (new: runtime diagram, data flow source → format →
                                                            store → evaluator → endpoint, event bus)
Distributions                          relay/distributions.md (new: flagd-full, flagd-sync, minimal/cloud/kubernetes,
                                                            vendor distribution; status per row)
Status and roadmap                     relay/status.md     (new: phase table, PoC use-case order, what works)
Try it                                 relay/try-it.md     (new: build from source, run flagd-full, gates)
Design
  Vision                               vision.md
  Requirements                         requirements.md
  Glossary                             glossary.md
  Use cases                            use_cases/*.md (+ use_cases.puml rendered as an image, or linked)
  Research: cross-vendor evaluation    research/cross-vendor-evaluation.md
  How this is built                    relay/how-its-built.md (new: AI Unified Process, the agent loop, links to
                                                            .claude/agents and .claude/plans on GitHub)
flagd (reference distribution)
  Introduction                         flagd/index.md      (today's index.md, moved — the only moved page)
  Quick start, Installation, Playground, Concepts, Architecture,
  Guides, OpenFeature Providers, Reference, Architecture decisions,
  FAQ, Troubleshooting                 (today's files, unchanged paths)
```

Moving `index.md` alone: today's `index.md` has relative links (`./reference/...`); from `flagd/index.md` they become `../reference/...`. That is one file to fix by hand.

New pages are short (one screen each) and link to the spec for detail rather than duplicating it. The spec is the contract; the site pages are an introduction and must not state behaviour the spec does not.

### 2.2 `mkdocs.yml` changes

- `site_name: OpenFeature Relay`, `site_description: A builder and runtime for flag relays — flagd as the reference distribution`.
- `repo_url: https://github.com/open-feature-forking/openfeature-relay`, `repo_name: open-feature-forking/openfeature-relay`.
- `exclude_docs`: drop the spec entries (D2); keep the not-yet-existing ones out or remove them.
- `nav`: as in 2.1.
- Logo: keep flagd's logo for now (the flagd section is most of the site) or use a plain text title; no new artwork in this plan.
- PlantUML: `use_cases.puml` is not rendered by mkdocs-material. Options: commit a rendered SVG next to it (generated by a `make` target), or link to the `.puml` on GitHub. Proposal: rendered SVG, regenerated by hand when the diagram changes.

### 2.3 README outline (draft on this branch)

1. Title, one-line pitch, the disclaimer (D1).
2. What it is: a builder and runtime for flag relays; the OCB analogy; flagd → flagd-full.
3. Why: the problem in four bullets (one binary with every sync and the OTel SDK, ~700 deps, no extension without forking, the embedded evaluator's spans).
4. Concepts: a small diagram (source → sync provider → configuration format → store → evaluator → service endpoint, plus event bus → event sink) and a table of component types.
5. Distributions: flagd-full, flagd-sync, minimal/cloud/kubernetes, vendor distribution.
6. Status: PoC; done (phase 1), in progress (UC-018), next (the PoC order); what you can run today (flagd-full = flagd built from this tree).
7. Try it: build and run flagd from source, evaluate with curl over flag evaluation and OFREP.
8. flagd: pointer to the flagd docs in `docs/` and to flagd.dev/open-feature/flagd, with credit.
9. How it's built: the spec, the agent loop (one paragraph, table of roles), links.
10. Contributing, license.

## 3. Public-readiness checklist

"Must" items block making the repository public; "should" items can follow. Checked items are done on branch `docs/relay-rewrite`.

### Personal and machine-specific details

- [x] **P1 (must)** `CLAUDE.md` gate command had a local Windows path (with a stale folder name). Now runs from the repository root; generic gates are in `CONTRIBUTING.md`.
- [x] **P2 (must)** `CLAUDE.md` named the sign-off address; now "the committer's own identity (DCO)". `.claude/plans/phase-1-review-a.md` likewise. Commit trailers keep the address; they are public with the history.
- [x] **P3 (must, D8)** The project lead's name removed from `CLAUDE.md`, `docs/vision.md` and `.claude/plans/*`.
- [x] **P5 (should)** `git grep -n -I -i -E 'Users[/\]si[m]on|/mnt/[c]/|si[m]on' -- . ':!CHANGELOG.md'` prints nothing.
- [x] **P6 (must)** `docs/research/cross-vendor-evaluation.md` cites public repositories and commits; the private testbeds are no longer referenced.
- [x] **P7 (should)** "flagd-builder" comments in `mkdocs.yml` and `.markdownlint-cli2.yaml` renamed.

### Not an official OpenFeature project

- [x] **P8 (must)** Disclaimer on the README and the site home.
- [x] **P8a (must)** `CODEOWNERS` names this repository's owner instead of `@open-feature/...` teams.
- [x] **P8b (must)** `CONTRIBUTING.md`: no community-meeting wording; "we follow the OpenFeature code of conduct"; Brigade artefact removed.
- [ ] **P8c (should)** The spec names upstream image names (`ghcr.io/open-feature/flagd...`) as the target; the README and site only tell readers to build from source. Nothing to change.
- [x] **P8d (should)** Issue templates: checked, they do not mention flagd. Nothing to change.
- [ ] **P8e (must, project lead)** Heads-up to the OpenFeature TC before going public.

### Workflows and settings

- [x] **P9 (must)** `release-please.yaml` and `dev-benches-page.yml` jobs run only in `open-feature/flagd` (`if: github.repository == ...`); their dependent jobs are skipped with them.
- [ ] **P10 (must, project lead)** Repository default workflow permissions are read-only, so "Validate PR title" fails with "Resource not accessible by integration". Set Settings → Actions → General → Workflow permissions to read and write (or grant `pull-requests: write` in `lint-pr.yaml`). Nothing to code.
- [ ] **P10a (must, project lead)** Check Settings → Secrets: no `RELEASE_PLEASE_ACTION_TOKEN` or `COSIGN_*` in this repository.
- [ ] **P10b (should, project lead)** Enable GitHub Pages with source "GitHub Actions" for `.github/workflows/docs.yaml`.
- [ ] **P11 (should)** `build.yaml` hard-codes an upstream-shaped `GOPATH`; left for upstream compatibility.
- [x] **P12 (should, D9)** `renovate.json` removed.

### Links

- [x] **P13 (must)** README links to upstream are deliberate.
- [x] **P14 (should)** `mkdocs.yml` `repo_url` points at this repository.
- [x] **P15 (should)** `mkdocs build --strict` passes, after fixing one broken link that already failed upstream (`reference/specifications/providers.md`).
- [x] **P16 (should)** `installation.md` says its releases are upstream flagd's.

### Content

- [x] **P17 (must)** `docs/research/` is published deliberately, after P6.
- [x] **P18 (should)** `requirements.md` retitled.
- [x] **P19 (should)** `CHANGELOG.md` note.
- [ ] **P20 (should)** `.golangci.bck.yml` is upstream's; leave it.
- [ ] **P21 (must, project lead)** Secret scan over this repository's own commits (`gitleaks detect --log-opts=b23f82e..main`) before flipping visibility.

## 4. Steps

Steps 1–5 are implemented as small commits on `docs/relay-rewrite` (one pull request). Gates: markdownlint, `mkdocs build --strict`, the spec gate for spec files. Step 6 is the project lead's, with P8e, P10, P10a and P10b.

| # | Step | Who | Gate |
|---|------|-----|------|
| 1 | README (this branch, after the project lead's edits) | orchestrator | markdownlint |
| 2 | `CLAUDE.md` trim + `CONTRIBUTING.md` rewrite, `CODEOWNERS`, issue templates (P1–P3, P7, P8a, P8b, P8d) | implementer | markdownlint |
| 3 | `mkdocs.yml` rename, nav, `exclude_docs`; new `relay/*` pages; move `index.md` to `flagd/index.md`; `installation.md` admonition; research cleanup (P6, P14, P16, P17) | implementer | `mkdocs build --strict`, link check |
| 4 | `requirements.md` title, `vision.md` wording (P3, P18) | orchestrator (spec change → `main`) | spec gate |
| 5 | Workflows: repository guard on release-please and benchmarks; Pages deploy workflow; delete `netlify.toml`, `sonar-project.properties` (P9, P10, D5) | orchestrator, after P10 is understood | CI green |
| 6 | Secret scan, final grep for local paths, flip visibility (P5, P21) | project lead | — |

## 5. Risks

- Upstream merges: every changed flagd file (`mkdocs.yml`, `CONTRIBUTING.md`, `CODEOWNERS`, workflows, `index.md`) will conflict on each `upstream/main` merge. Keeping flagd pages in place (2.1) and guarding workflows with `if:` rather than deleting them keeps the conflict surface to a handful of files.
- New Relay pages can drift from the spec. They link to the spec rather than restating rules, and the status page is updated in the same PR that moves a use case's `**Status:**`.
- The draft README states what works today; it must change with every merged slice. The status section is short on purpose.
