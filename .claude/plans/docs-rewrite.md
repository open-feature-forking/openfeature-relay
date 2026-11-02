# Docs rewrite plan: present OpenFeature Relay first, flagd as its reference distribution

**Status: draft for Simon.** Base: `main` @ e68610d. The README draft is in `README.md` on this branch.

Goal: a visitor who lands on the repository or the site learns in one screen what OpenFeature Relay is, that it is a proof of concept, what works today, and that flagd-full is today's flagd. Then the repository can be made public without leaking personal workflow details or implying it is an official OpenFeature project.

Not in scope: changing the spec's content, code, or the flagd docs' substance. The flagd pages are moved, not rewritten.

## 0. Decisions for Simon

| # | Question | Proposal |
|---|----------|----------|
| D1 | Is "OpenFeature Relay" the public name, given it lives outside the `open-feature` org and is not an OpenFeature project? | Keep it, with a disclaimer at the top of the README and the site home: an independent incubator by a flagd maintainer, not an official OpenFeature or CNCF project. Ask the OpenFeature TC before the repo goes public (a message, not a PR). |
| D2 | Spec (`vision`, `requirements`, `glossary`, `use_cases*`) on the site or only in the repo? | On the site, under "Design", rendered read-only. It is the most interesting part for people evaluating the idea. Drop the `exclude_docs` entries for them; keep `docs/research/` excluded or move it (D4). |
| D3 | `.claude/` (agents, plans) and `CLAUDE.md` in the public repo? | Keep both: the agent loop is part of what is being shown. Trim `CLAUDE.md` of local paths and personal details (checklist P1–P3) and move the machine-specific gate commands into `CONTRIBUTING.md` as generic Linux/WSL instructions. Plans stay as they are; they are history. |
| D4 | `docs/research/cross-vendor-evaluation.md` | Keep, under "Design → Research", after removing references to local checkouts and private testbeds (P6). It is the strongest argument for the vendor distribution. |
| D5 | Site hosting | GitHub Pages from this repository (a `docs` workflow with `mkdocs gh-deploy`), not Netlify; flagd.dev stays flagd's. Delete `netlify.toml`. |
| D6 | flagd docs: keep all, or prune what only concerns upstream flagd's release process? | Keep all user-facing pages under "flagd (reference distribution)"; links in them that point to flagd.dev keep working, because flagd.dev documents the same behaviour. Upstream-process pages (ADRs) move under "flagd → Architecture decisions" unchanged. |
| D7 | Badges and contributors image | Drop the upstream flagd CI, goreportcard and Killercoda badges; add this repo's build badge only once CI runs green publicly. Credit flagd's contributors in prose with a link to upstream's contributors graph. |

## 1. Inventory

### 1.1 Top-level files

| File | Today | Action |
|------|-------|--------|
| `README.md` | flagd's README (logo, badges pointing at `open-feature/flagd`, quick start with `ghcr.io/open-feature/flagd`, contrib.rocks for upstream) | Replace with the Relay README (draft on this branch). flagd's quick start moves to the site's flagd section (it is already `docs/quick-start.md`). |
| `CLAUDE.md` | Working agreement, gates with `/mnt/c/Users/simon/...` (and the stale folder name `OpenFeature Relay`), sign-off address, "Simon approves" | Trim (P1–P3). Keep the loop, phases, PoC order and conventions. |
| `CONTRIBUTING.md` | flagd's: code of conduct link to open-feature/.github, `make test`, manual testing with curl/grpcurl, DCO, conventional PR titles, markdownlint | Rewrite the intro (what contributions fit a PoC; open an issue first), keep Development, Manual testing, DCO and lint sections. Add the gate table from `CLAUDE.md` in generic form (Go toolchain pin, `GOWORK` in worktrees, `GOPATH` for lint). Remove the Brigade reference in the DCO text (copy-paste artefact from upstream). |
| `CODEOWNERS` | `@open-feature/flagd-maintainers @open-feature/maintainers`, "Managed by Peribolos" | Replace with this repo's owner(s); the upstream teams do not exist in `open-feature-forking`. |
| `CHANGELOG.md` | flagd's release history | Keep (history); add a one-line note at the top that it is upstream flagd's changelog up to the branch point. |
| `.release-please-manifest.json`, `release-please-config.json` | flagd's release config | Keep; the workflow is disabled (P9). |
| `netlify.toml` | flagd.dev's Netlify build | Delete if D5 is accepted. |
| `sonar-project.properties` | flagd's SonarCloud project | Delete, or point at nothing; it names flagd's project key. |
| `renovate.json` | flagd's Renovate config | Keep only if Renovate is installed on this repo; otherwise delete to avoid confusion. Decision for Simon (P10). |
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

Each item: what, where, action. "Must" items block making the repository public; "should" items can follow.

### Personal and machine-specific details

- [ ] **P1 (must)** `CLAUDE.md` gate command: `cd /mnt/c/Users/simon/openfeature/OpenFeature Relay` — a local Windows path, and stale (the folder is `flagd-builder`). Replace with `cd <repo root>` and move the WSL/Windows notes into a "Windows" subsection of `CONTRIBUTING.md`.
- [ ] **P2 (must)** `CLAUDE.md` Conventions: "signed off as `simon.schrottner@flagsmith.com`". Replace with "signed off with the author's own identity (DCO)". The address is also in `.claude/plans/phase-1-review-a.md:53` and in every commit's `Signed-off-by`; the commits are public anyway once the repo is, and the plan line is history — decide whether to leave it.
- [ ] **P3 (should)** `CLAUDE.md` and `docs/vision.md` name Simon as approver ("Simon approves each plan", "until Simon decides to take it upstream"). Public is fine if Simon is comfortable; otherwise use "the project lead". Proposal: keep the name in `CLAUDE.md` (it is the working agreement), reword `vision.md` to "until the incubator's owner proposes it upstream".
- [ ] **P4 (must)** `CLAUDE.md` Containment section and the "Linked sessions"/side-session wording are fine to publish, but the containment rule should be re-read once public: "no comments anywhere except this repository" still holds; nothing to change.
- [ ] **P5 (should)** `.claude/plans/*.md` mention local checkouts (`flagd-builder/go.work`, worktree paths). Leave as history, or sweep for absolute paths: `git grep -n -E 'C:\\\\|/mnt/c|Users/simon'` must print nothing outside `CLAUDE.md` after P1. Today it prints only `CLAUDE.md:65`.
- [ ] **P6 (must)** `docs/research/cross-vendor-evaluation.md` Sources table: "this checkout (`OpenFeature Relay`, branch `spec`)", "local `go-sdk-contrib/providers/`, working tree", "local `flagsmith-testbed`, `goff-testbed` … `FINDINGS.md` in each". Replace local references with public commit URLs; say the testbeds are private notes (or publish them). Also drop "branch `spec`" (gone).
- [ ] **P7 (should)** `.markdownlint-cli2.yaml` and `mkdocs.yml` comments say "flagd-builder"; rename to "OpenFeature Relay".

### Not an official OpenFeature project

- [ ] **P8 (must)** README and site home carry the disclaimer (D1). The org name `open-feature-forking` and the project name both suggest OpenFeature ownership; the disclaimer has to say plainly that it is not.
- [ ] **P8a (must)** `CODEOWNERS` names `@open-feature/...` teams — replace.
- [ ] **P8b (must)** `CONTRIBUTING.md` points at OpenFeature's code of conduct and community meetings as if this were an OpenFeature project. Keep the CoC link ("we follow the OpenFeature code of conduct") but drop "join our community meetings".
- [ ] **P8c (should)** Image names in the spec (`ghcr.io/open-feature/flagd`, `ghcr.io/open-feature/flagd-<distribution>`, UC-002 BR-006, UC-012 BR-012) describe the upstream target. Fine in a spec, but the README must not tell anyone to pull images this repo does not publish under that name (the draft only tells them to build from source).
- [ ] **P8d (should)** Issue templates say "flagd"; reword (1.1).

### Workflows

- [ ] **P9 (must)** `release-please.yaml` and `dev-benches-page.yml` are disabled in repository settings, not in the files (`CLAUDE.md`: "do not enable them"). In a public repo anyone forking gets them enabled. Proposal: add `if: github.repository == 'open-feature/flagd'` to their jobs, so they are inert here and in forks while the files stay mergeable with upstream. `release-please.yaml` also references `RELEASE_PLEASE_ACTION_TOKEN` and `COSIGN_*` secrets that this repo must never get.
- [ ] **P10 (must) the workflow token issue.** Not found in the repository or the plans. Pushing changes under `.github/workflows/` needs a token with the `workflow` scope; the local `gh` login (account `aepfli`) has `gist, read:org, repo, workflow`, so that is not it from this machine. Simon to say what the issue is (a fine-grained token on another machine? a token stored as a repository secret?). Related, checked: `lint-pr.yaml` already runs on `pull_request_target`, so it keeps working for fork PRs once public; it only lints titles, so `pull_request_target` is safe here. Any repository secrets copied from upstream setups (`RELEASE_PLEASE_ACTION_TOKEN`, `COSIGN_*`) must not exist in this repository — check Settings → Secrets before going public.
- [ ] **P11 (should)** `build.yaml` hard-codes `GOPATH: /home/runner/work/open-feature/flagd` and pushes on `feature/workflows`. Works (it is just a path on the runner), but looks odd; leave for upstream compatibility.
- [ ] **P12 (should)** `renovate.json`: either install Renovate on this repo or delete the file; a public repo with a dead config confuses. Upstream merges bring the file back, so prefer installing nothing and leaving it (it does nothing without the app). Decision for Simon.

### Links

- [ ] **P13 (must)** README: every link to `open-feature/flagd` or flagd.dev is either deliberate ("upstream flagd") or replaced. The draft's links are deliberate.
- [ ] **P14 (should)** `mkdocs.yml` `repo_url` / `edit_uri` → this repo (2.2), otherwise "edit this page" opens upstream.
- [ ] **P15 (should)** 38 pages under `docs/` link to flagd.dev or `open-feature/flagd`. They document flagd's behaviour, which flagd-full keeps, so leave them. Run a link check (`mkdocs build --strict` plus a link checker) after the nav change and fix only relative links broken by the move.
- [ ] **P16 (should)** `docs/installation.md` admonition (1.3).

### Content

- [ ] **P17 (must)** `docs/research/` is published by `mkdocs build` today though the comment says the spec is not part of the site — fix with D2/D4 either way.
- [ ] **P18 (should)** `requirements.md` title "flagd Builder" → "OpenFeature Relay".
- [ ] **P19 (should)** `CHANGELOG.md` note (1.1).
- [ ] **P20 (should)** Delete `.golangci.bck.yml` if it is not used (check upstream: it exists there too — then leave it).
- [ ] **P21 (should)** History: the repo carries flagd's full history, which is public already. Nothing in this repo's own commits was found that must not be public, but run a secret scan (`gitleaks detect` over `main..` since the branch point) before flipping the switch.

## 4. Steps

Each is a small PR against `main`. All are docs or config; no gates beyond markdownlint, `mkdocs build --strict` and the spec gate where spec files change.

| # | Step | Who | Gate |
|---|------|-----|------|
| 1 | README (this branch, after Simon's edits) | orchestrator | markdownlint |
| 2 | `CLAUDE.md` trim + `CONTRIBUTING.md` rewrite, `CODEOWNERS`, issue templates (P1–P3, P7, P8a, P8b, P8d) | implementer | markdownlint |
| 3 | `mkdocs.yml` rename, nav, `exclude_docs`; new `relay/*` pages; move `index.md` to `flagd/index.md`; `installation.md` admonition; research cleanup (P6, P14, P16, P17) | implementer | `mkdocs build --strict`, link check |
| 4 | `requirements.md` title, `vision.md` wording (P3, P18) | orchestrator (spec change → `main`) | spec gate |
| 5 | Workflows: repository guard on release-please and benchmarks; Pages deploy workflow; delete `netlify.toml`, `sonar-project.properties` (P9, P10, D5) | orchestrator, after P10 is understood | CI green |
| 6 | Secret scan, final grep for local paths, flip visibility (P5, P21) | Simon | — |

## 5. Risks

- Upstream merges: every changed flagd file (`mkdocs.yml`, `CONTRIBUTING.md`, `CODEOWNERS`, workflows, `index.md`) will conflict on each `upstream/main` merge. Keeping flagd pages in place (2.1) and guarding workflows with `if:` rather than deleting them keeps the conflict surface to a handful of files.
- New Relay pages can drift from the spec. They link to the spec rather than restating rules, and the status page is updated in the same PR that moves a use case's `**Status:**`.
- The draft README states what works today; it must change with every merged slice. The status section is short on purpose.
