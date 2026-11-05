# Step 17 plan: publish the standard distributions and flagd-testbed to ghcr from `main` (issue #216)

**Status: revision 2.**
- It resolves `step-17-opposer.md`: 1 blocker, 5 major and 8 minor, all adopted or answered (section 8).
- Four questions are open for the project lead: Q2, Q3, Q5 and Q6.
- Q1 and Q4 are settled as planner defaults the lead can overrule.

**Decided by the project lead (#216):**
- **Images:** flagd-full, flagd-minimal, flagd-minimal-otel, flagd-cloud, flagd-kubernetes and flagd-sync, each in standard and FIPS, plus flagd-testbed. All are linux/amd64 + linux/arm64, under `ghcr.io/open-feature-forking/`.
- **Not pushed:** flagd and flagd-proxy (they would be confused with the official images), and the Flagsmith distribution.
- **Tags:** `:main`, which moves, and `:sha-<short>`, which is immutable. No `latest`, no semver.
- **Push only from `main`.** This lifts the standing "never push images" rule deliberately; release-please stays disabled.
- **The token (later decision).** A separate PR, **W**, outside this step:
  - adds a top-level `permissions` block to every workflow;
  - adds the K2 check for it;
  - comes with the lead switching the repository's default workflow token to read.

  P merges only after W and the setting change.

**Base:** `main` @ a00e4b0d. Job counts are from run 42079716561 (afa094e1). "R" is `github.com/open-feature-forking/openfeature-relay`.

## 1. What exists, and what is missing

| Image | Built today | Where | Gap |
|---|---|---|---|
| flagd-full | as `flagd`, from `flagd/build.Dockerfile` | `images` part | It is built under flagd's name. Under its own name it needs the reference Dockerfile with `distributions/flagd-full/build` (section 7). |
| flagd-minimal, standard and FIPS | multi-arch OCI layout with provenance and SBOM | `distributions` part | The push is missing. The runtime checks run on a separate `--load` build, not on the layout. |
| minimal-otel, cloud, kubernetes, sync | **no image**, one linux/amd64 archive each | `distributions` part | Both images, multi-arch, are missing. |
| flagd-testbed | linux/amd64 `docker build` with the default driver (`Makefile:492`), path-gated (`build.yaml:841`) | `testbed` job | Multi-arch is missing, and there is no provenance or SBOM. The `certs` stage is `FROM alpine/openssl` with no tag or digest. `certs` and `RUN mkdir flags` run on the target platform, so arm64 needs QEMU today. |

**Load today:**
- **Runs:** `build` ran on 76, 65 and 58 `main` pushes a day (2026-11-03 to 11-05). Of 74 and 72 first-parent commits on 11-03 and 11-04, 48 and 37 touched image inputs (Go, `go.mod`/`go.sum`, a Dockerfile, `distributions/`, `builder/`).
- **Jobs:** a `main` run starts 18 jobs, and the slowest dry-run part (`distributions`) takes 16 min.
- **The queue:** the free plan runs 20 jobs at once org-wide. Jobs above that wait; they do not fail, but they slow every PR.

## 0. Questions for the project lead

- **Q1, build once, push from the layouts. (settled; the opposer confirmed the split)**
  - **The build.** Every image is built once, without registry access, as a 2-platform OCI layout with provenance and SBOM.
  - **The checks** run on that layout:
    - structure (name, platforms, attestations, labels);
    - the runtime (entrypoint, user, `version --verbose`, the testbed's contract and suites), on the amd64 image loaded from the layout with `skopeo copy oci-archive:… docker-daemon:…`.

    arm64 is checked only structurally.
  - **The push.** `publish-images` pushes by digest. It holds the only `packages: write` and runs no build: only `publish-images.sh`, a pinned login, and the attestation (Q2).
  - **Rejected:** a rebuild with a cache (a second build), and pushing from inside the dry run (the token would sit beside the build).

- **Q2, signing, base-image updates and scanning. (open)**
  - (a) **GitHub artifact attestations, Dependabot for the base images, and a Trivy report.**
    - `actions/attest-build-provenance` signs each pushed digest, beside buildx's provenance and SBOM; users run `gh attestation verify oci://… -R open-feature-forking/openfeature-relay`.
    - A `.github/dependabot.yml` covers the `docker` ecosystem, weekly, for `builder/reference/Dockerfile` and the testbed Dockerfile, so the digest pins move.
    - Trivy scans each layout in the unprivileged build job and only reports.
  - (b) As (a), without Dependabot: the base digests are bumped by hand, in a step of their own.
  - (c) cosign keyless in place of attestations, plus Dependabot and Trivy as in (a).
  - (d) Nothing signed: buildx's in-index provenance and SBOM only.
  - **Recommended:** (a).
    - Attestations and cosign keyless both write a public Sigstore transparency-log entry naming this repository; the images are public anyway.
    - Dependabot is a new bot in this repository. Its PRs follow the loop like any other.

- **Q3, `sha-` retention. (open)**

  **The numbers,** with Q6 (b):
  - ~8 publishes a day × 13 tags is ~100 `sha-` tags a day.
  - In ghcr every multi-arch tag also creates untagged versions: 2 platform manifests, 2 buildx attestation manifests and the Q2 attestation. That is ~600 package versions a day across the 7 packages.
  - With Q6 (a) it is ~5× that.

  The options:
  - (a) **Keep everything during the PoC.** Public storage is free; the cost is a long version list. "Delete untagged" is never used: it breaks every multi-arch tag and its attestations. The question comes back at the end of the PoC, or at 30 days.
  - (b) **Delete `sha-` tags older than 14 days.** A scheduled `cleanup-images` job, a second writer under Q5, deletes a tagged index only together with the untagged versions that no kept index references.
    - Its first run is checked on ghcr: `crane manifest` of a kept tag resolves both platforms, and `gh attestation verify` still passes.
    - `registry:2` does not model ghcr's versions, so no rehearsal covers it.
  - **Recommended:** (a). Deletion is the one step here that can break a published image, and at Q6 (b)'s rate the list stays readable for weeks.

- **Q5, who can push. (open; W fixes the default token)**

  **What holds after W:**
  - Only `publish-images` requests `packages: write`.
  - Fork PRs get a read-only token whatever they request.
  - `lint-pr.yaml`'s `pull_request_target` gets its own block in W.

  **What W cannot stop:** any branch pushed to this repository can request `packages: write` in its own copy of `build.yaml`, on `push` or on a same-repository PR. ghcr grants write to this repository's workflows, per repository, not per branch. `main` has no protection or rulesets, so the people who can do that are exactly the people who can push to `main` today.

  The options:
  - (a) **Accept and document it for the PoC.**
    - The CLAUDE.md rule: no session requests `packages: write` outside `publish-images` or rehearses a push on a branch.
    - K2 pins the one job that may request it.
    - R1 says plainly: *anyone who can push a branch here can push these images*.
  - (b) **Enforce `main` with an environment and its own credential.**
    - An environment `ghcr` with a `main`-only deployment branch policy holds the push credential. That is a classic PAT with `write:packages` of a machine account; ghcr does not take fine-grained PATs, and whether it takes a GitHub App installation token is to be checked in P.
    - `publish-images` declares `environment: ghcr`.
    - After the first push, an org owner lowers each package's Actions access for this repository to Read, so `GITHUB_TOKEN` alone can no longer push from any branch.
    - Q3 (b)'s cleanup would use the same environment.
  - **Recommended:** (a) for the PoC.
    - (b) adds a long-lived credential bound to an account outside the repository, plus a manual setting on 7 packages, to guard against the people who can already push to `main`.
    - (b) is the right shape once `main` is protected, or before anything is released.

- **Q6, how often to publish. (open; #216 says `:main` moves with every push, and the load says otherwise)**
  - (a) **Every `main` push that touches image inputs.** That is ~40–50 publishes a day. Each runs 18 + 2 image-build parts + 2 publish jobs = 22 jobs, above the 20-job org limit, so PRs queue behind `main`. It uploads ~20–50 GB of layouts a day (one-day retention).
  - (b) **Throttled.**
    - A `main` push publishes when the image inputs changed **and** `:main` is more than 3 hours old. A small `publish-gate` job reads `:main`'s `org.opencontainers.image.created` anonymously.
    - A `workflow_dispatch` on `main` publishes at once.
    - Only publishing runs start the image-build parts and upload layouts: ~8 a day, ~4–8 GB.
    - `:main` is at most ~3 h plus one build behind the head; `:sha-` tags exist only for published commits.
  - (c) Nightly: a scheduled run builds and publishes `main`'s head. It is a run of its own, so for that one commit the build happens twice.
  - **Recommended:** (b). It keeps "from `main`, build once", gives a fresh image within hours, and adds jobs to ~8 runs a day instead of ~45.

- **Q4, the stamped version and the "proof of concept" marking. (settled)**
  - **The version:** on a publishing run the build stamps `0.0.0-main.<short>`, so `version` prints `v0.0.0-main.abc1234`: a pre-release that sorts below every release.
  - **The marking:** every image, testbed included, carries labels and `index:`/`manifest:` annotations:
    - `org.opencontainers.image.title`: the image name;
    - `org.opencontainers.image.description`: "Proof of concept from the OpenFeature Relay incubator, built from main. Not an official OpenFeature image; not for production.";
    - `org.opencontainers.image.source`, `org.opencontainers.image.revision` (the full sha), `org.opencontainers.image.version` (`main-<short>`), `org.opencontainers.image.created`, `org.opencontainers.image.licenses=Apache-2.0`.

    There is no `vendor`.
  - **How:** `--label`/`--annotation` in the dry-run scripts. No change to flagd's or flagd-proxy's Dockerfile (K3).

**Planner defaults (the lead can overrule):**
- **Names.** `ghcr.io/open-feature-forking/<name>`, with `<name>` = `flagd-full`, `flagd-minimal`, `flagd-minimal-otel`, `flagd-cloud`, `flagd-kubernetes`, `flagd-sync` or `flagd-testbed`.
  - flagd-full is published as `flagd-full`, not as BR-012's `flagd` (#216). That is a PoC deviation, recorded in UC-012's gaps.
  - FIPS is a tag suffix in the same package (`:main-fips`, `:sha-<short>-fips`), as BR-012 does.
- **`<short>`** is `${GITHUB_SHA::7}` everywhere: tags, version, docs.
- **Visibility.** Public, and linked to this repository through `org.opencontainers.image.source`. After the first push an org owner sets the 7 packages to public once, and D checks them with an anonymous pull.
- **Permissions.** `publish-images` alone requests:
  - `packages: write`;
  - `id-token: write` and `attestations: write`, under Q2 (a) or (c).

  `publish-gate` requests nothing beyond W's read default, because it reads `:main` anonymously.
- **When `publish-images` runs.**
  - Only when `github.ref == 'refs/heads/main'` and `github.repository == 'open-feature-forking/openfeature-relay'`, on `push` (or `workflow_dispatch` under Q6 (b)), and only after `publish-gate` says so.
  - A PR, a fork and `FULL_CI` never publish.
- **The destinations are hard-coded.**
  - `publish-images.sh` holds the 7 destination names and refuses any other.
  - It names the source ref of each layout explicitly. A layout carries two `ghcr.io/open-feature/…` refs, which are never read as destinations.
- **`sha-` tags have no concurrency group.** Each push of a `sha-` tag is independent and idempotent.
- **`:main` moves by compare-and-set.** A separate `move-main` job (`needs: publish-images`, `concurrency: move-main`) reads the revision that `:main` points at now. It moves the tag only if that commit is an ancestor of `github.sha` (`git merge-base --is-ancestor`, with full history). A cancelled pending `move-main` is harmless, because the next one does the same check.
- **The testbed builds once.**
  - On a publishing run the `testbed` job builds the 2-platform layout, with provenance and SBOM, and loads its amd64 image from the layout.
  - The contract and the suites run on that image: `make testbed-contract-both testbed-suites` gains `TESTBED_RELAY_IMAGE`, which skips `testbed-relay-image`.
  - The Dockerfile changes:
    - `certs` runs `FROM --platform=$BUILDPLATFORM alpine/openssl@sha256:<digest>`;
    - `RUN mkdir flags` becomes `WORKDIR /flags` followed by `WORKDIR /`.

    The final stage then only copies.
  - When the `testbed` job is skipped, `publish-images` pushes the other images and the step summary says the testbed was not published.
- **Base pins.** The pin test (today `TestReferenceDockerfilePins`) extends to every `FROM` of the published Dockerfiles: a digest on each.
- **K2.** `test_nothing_is_pushed_or_logged_in` becomes `test_only_publish_images_pushes_or_logs_in`, over every file in `.github/workflows/`. It finds:
  - `docker/login-action`;
  - `--push`, `push: true`, `push=true` and `type=registry`;
  - `docker push`, `docker login`, `crane`, `skopeo`, `oras`;
  - `push-to-registry`;
  - `packages: write`.

  They may appear only in `publish-images` (and in `move-main` for `crane`/`skopeo`). The one exception is `release-please.yaml`'s login, allowlisted by its existing hash. `test_jobs` gains `publish-gate`, `image-build`, `publish-images` and `move-main`. W owns the top-level `permissions` check.
- **Docs.**
  - **`README.md:9`** and **`try-it.md`'s first line** say: no releases; images built from `main` are published for testing, as a proof of concept, not official OpenFeature images; this is not an official OpenFeature or CNCF project.
  - **`try-it.md` gains "Pull an image",** with:
    - `docker run` of `flagd-minimal:main` with the sample flags mounted;
    - `flagd-testbed:main`;
    - `:sha-<short>` for a fixed build;
    - Q2's verify command;
    - the note that the testbed's certificate proves nothing (R5).

## 2. Spec change (for D)

- **UC-002:** the gap row "Steps 7–8, A5 and A6 … Nothing is pushed during the proof of concept" becomes "Steps 7–8 run from `main` (`publish-images`) for the standard distributions and flagd-testbed under `ghcr.io/open-feature-forking/`; A5 and A6 remain the Container Registry's behaviour".
- **UC-012:**
  - the dry-run paragraph says images are built and checked for all six standard distributions on a publishing run;
  - a new gap line covers the PoC names (flagd-full as `flagd-full`), the `:main`/`:sha-` tags in place of BR-012's, and no release.
  - BR-012 is unchanged.
- **No new FR or use case.** `/spec-review` runs in D.

## 3. Tests

| Test | Where | Proves |
|---|---|---|
| `test_only_publish_images_pushes_or_logs_in` | K2, every workflow | the push surface, its `if:`, the one allowlisted login (section 0) |
| `test_publish_jobs` | K2 | `publish-images` needs `image-build`, `release-dry-run` and `testbed`, and runs no build step; `move-main` has `concurrency: move-main`; `publish-gate` drives `image-build` |
| `test_release_dry_run_parts` | K2, updated | `full` still covers today's archive and image lists; `image-build`'s parts cover the 13 images |
| `BuildDockerfiles` | K3 | flagd's and flagd-proxy's Dockerfiles unchanged |
| the base pin test | `test/image` | every `FROM` of the published Dockerfiles carries a digest |
| the layout checks, per image | dry-run scripts | name, `linux/amd64 linux/arm64`, attestation manifests, provenance, SBOM, Q4's labels and annotations, for 13 images (testbed included); entrypoint, user and `version --verbose` on the amd64 image loaded from the layout |
| the publish rehearsal | `publish-images.sh` against `registry:2`, in WSL and on a `release-check` run | push by digest (the pushed digest equals the layout's); the tags; attestations survive; a destination outside the 7 is refused; a missing testbed layout is skipped and reported; an older commit published after a newer one leaves `:main` on the newer |
| `publish-gate` | a unit test of its script | inputs unchanged gives no publish; `:main` younger than 3 h gives no publish; a missing `:main` or `workflow_dispatch` gives a publish |
| the testbed on its layout | `testbed` job, publishing run | contract and suites green on the image that is pushed |
| the first `main` publish | the reviewer, after P merges | 7 public, linked packages; both platforms; `version --verbose` prints `v0.0.0-main.<short>`; Q2's verify passes; flagd and flagd-proxy absent from the namespace |

## 4. PR slicing

| PR | Model | Base | Content | Starts |
|---|---|---|---|---|
| W | (the lead's separate PR) | `main` | top-level `permissions` in every workflow, its K2 check, the default token set to read | now, outside step 17 |
| B | implementer | `main` | The builds, with no push. `publish-gate`; `image-build` (parts below); flagd-full under its own name; the four missing images; Q4; the layout-based runtime checks; the testbed Dockerfile, its layout and `TESTBED_RELAY_IMAGE`; the base pins; Trivy (Q2); the artifacts. It measures every part on a `workflow_dispatch` | after the decisions |
| P | implementer | B, and W merged | `publish-images.sh` and its rehearsal, `publish-images`, `move-main`, the attestation (Q2), Dependabot (Q2), K2's push test | after B exists (stacked); merges after W |
| D | mechanic | P merged and its first publish checked | README, `try-it.md`, UC-002, UC-012, CLAUDE.md (below), `/spec-review`, #216 | last |

**`image-build`'s parts** (estimates from step 15's per-compile times with `-a`; B measures):

| Part | Images | Compiles | Estimate |
|---|---|---|---|
| A | flagd-full and flagd-sync, each standard and FIPS | 8 | ~11 min |
| B | flagd-cloud, flagd-kubernetes and flagd-minimal-otel, each standard and FIPS | 12 | ~13 min |

flagd-minimal stays in the `distributions` part; the testbed stays in `testbed`.

**Budget:**
- no part longer than 16 min (today's `distributions`);
- no more than 22 jobs on a publishing run.

If B goes over, B splits into one part per distribution, and the job count goes back to the lead.

**CLAUDE.md in D:**
- Containment gains: "Images go only to `ghcr.io/open-feature-forking/`, only from `build.yaml`'s `publish-images` and `move-main` jobs on `main`, and only the 7 images of #216. No session logs in to a registry, requests `packages: write` elsewhere, rehearses a push on a branch, or runs `make docker-push-flagd`."
- The CI paragraph adds the publishing run (Q6).

The critical path is B → P (with W merged) → the first publish → the org owner's visibility click → D.

## 5. Gates

| Gate | Command | PRs |
|---|---|---|
| Workflow tests | `python3 .github/scripts/test_workflows.py` | B, P |
| Dry run with image build | `workflow_dispatch` of `build` on the branch (no publish off `main`) | B |
| Dry run, reduced | the `release-check` label | B, P |
| Publish rehearsal | `publish-images.sh` against `registry:2` | P |
| Testbed | `make testbed-contract-both testbed-suites`, from the layout | B |
| Image tests | `test/image` (CI) | B |
| First publish | section 3's last row | after P |
| Spec | `spec_lint.py --docs docs` on Windows, and `/spec-review` | D |

## 6. Risks

- **R1, who can push (Q5).** After W, only `publish-images` requests `packages: write`, and K2 pins that. *Anyone who can push a branch here can still push these images*, by requesting the permission in a branch's workflow. Under Q5 (a) that is accepted for the PoC and written in CLAUDE.md; Q5 (b) closes it.
- **R2, confusion with official images.** Against it: a different owner, no `latest`, no semver, a pre-release version, the description, the README's disclaimer, and no `flagd` or `flagd-proxy` package. Nothing stops someone running `flagd-full:main` in production.
- **R3, the queue (Q6).** Under (b), ~8 runs a day go from 18 to 22 jobs. Under (a), ~45 do, and PRs wait behind `main`.
- **R4, artifacts.** ~0.5–1 GB of layouts per publishing run, kept for one day: ~4–8 GB a day under Q6 (b). B measures.
- **R5, the testbed's certificate.** It is generated at build time, valid for 365 days, and signed with `custom-ca.key` from the public test-harness submodule. The CA key is public by design, so the certificate proves nothing. `try-it.md` says so.
- **R6, a red `main`.** `publish-images` needs the builds, so a red run publishes nothing. `move-main`'s ancestor check keeps `:main` on the newest green published commit.
- **R7, stale bases.** Pinned digests only move with Q2's Dependabot (or by hand under (b)). Trivy makes known CVEs visible before each push.
- **R8, drop-in.** No change to flagd's image, name or Dockerfile (K1, K3), or to the release path (`release-please.yaml`, `release-build.sh`).

## 7. Considered

- **Pushing flagd's existing layout as `flagd-full`.** It would save 4 compiles. But Q4's labels would change flagd's image configuration (K1's `image-config.golden`), and flagd's image must stay drop-in. The published flagd-full is therefore built from the reference Dockerfile, which is how every other distribution is built.
- **Images that copy `release-build.sh`'s linux binaries** would really build once. They leave upstream's Dockerfile path, and only flagd-minimal has arm64 archives today, so they are not proposed.
- **Publishing only by digest change** does not work: the stamped commit changes every digest. Q6 (b) gates by paths and age instead.
- **A separate `publish.yaml` on `workflow_run`.** It needs artifacts across workflows; `needs:` inside `build.yaml` is simpler.

## 8. Changelog

### Revision 2 (`step-17-opposer.md`)

| Finding | Severity | Outcome |
|---|---|---|
| 1 `packages: write` in every default token; any branch can ask for it | blocker | Adopted. The default token and every workflow's block are W, the lead's separate PR (verified: `default_workflow_permissions: write`, `lint-pr.yaml:4` is `pull_request_target`). R1 corrected. The branch residual is Q5, with the environment-and-credential option. K2 covers every workflow |
| 2 The pushed testbed is not the tested one; the job is path-gated | major | Adopted. One 2-platform build, amd64 loaded from the layout, contract and suites on it (`TESTBED_RELAY_IMAGE`); a skipped testbed publishes nothing and says so |
| 3 Load off by ~3× | major | Adopted, re-measured: 58–76 runs a day, 37–48 commits a day touching image inputs, 18 jobs per run. The 20 limit is org concurrency (jobs queue), not a per-run cap. New Q6 (throttled publishing), parts split with a budget, R3/R4 redone, the build-once binary alternative in section 7 |
| 4 Concurrency drops `sha-` tags; `:main` stalls | major | Adopted. No group on `sha-` pushes; `move-main` by compare-and-set on the ancestor check; rehearsal test |
| 5 Retention numbers; "delete untagged" breaks images | major | Adopted. Q3 with the numbers; never delete untagged; deletion only with referenced versions; first cleanup checked on ghcr |
| 6 Unpinned `alpine/openssl`; no base updates; no scan | major | Adopted. Pinned by digest, pin test over every `FROM`; Dependabot and Trivy in Q2 |
| 7 Runtime checks on a separate build | minor | Adopted. Run on the amd64 image from the layout; arm64 structural only, stated |
| 8 flagd's layout as flagd-full | minor | Adopted as section 7 |
| 9 K2 over every workflow and every push form | minor | Adopted. `docker-push-flagd` named in the CLAUDE.md rule |
| 10 README says no images | minor | Adopted, in D |
| 11 Hard-coded destinations | minor | Adopted, with a refusal test |
| 12 Rebase on today's `main` | minor | Adopted: a00e4b0d, counts from afa094e1 |
| 13 Fixed `<short>` | minor | Adopted: `${GITHUB_SHA::7}` |
| 14 Testbed without provenance; CA key public | minor | Adopted: provenance, SBOM and checks; R5 |

Revision 1's Q1 and Q4 are now settled defaults. The visibility half of Q3 became a planner default, since private packages would defeat #216.
