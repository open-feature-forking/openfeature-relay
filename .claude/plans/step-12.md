# Step 12 plan: Package Distribution as Image, Release Distributions and Component Modules, Verify Change Against Distributions (UC-002, UC-012, UC-013)

**Decision (2026-11-04):** revision 2 approved by the project lead; every recommendation (Q1–Q17) accepted.

**Status: revision 2.** It resolves `step-12-opposer.md` (0 blockers, 4 major, 5 minor); each finding's outcome is in section 8. Section 0 holds the questions for the project lead. Each recommendation leaves today's flagd and flagd-proxy images and release as they are on `main`.

- **Changed recommendations:** Q8 (one standard distribution through the full path), Q9 (no release configuration change), Q10 (a placeholder version), Q14 (NFR-001 kept), Q15 (inside P3), Q16 (inside `upgrade-test`; the dry run gets a changed-files gate) and Q17 (two terms).

**Base.** `main` @ 9ead44a3. Step 10's stack D (the four standard manifests, `distributions/<x>/build/`, the FIPS compile per manifest D4) and P3 (flagd-sync end to end) are not merged yet. Issues: UC-002 #14, UC-012 #24, UC-013 #25. "R" is `github.com/open-feature-forking/openfeature-relay`.

**Scope rule (project lead).** Each use case gets the smallest proof of its main success scenario. Everything else is a named accepted gap. Nothing is released, pushed, signed or tagged for real, and release-please stays disabled; its configuration does not change. Every image and release check is a dry run.

**What exists.**

- **Images.**
  - `flagd/build.Dockerfile` and `flagd-proxy/build.Dockerfile` use the repository root as build context.
  - Each runs `go work init` over a hard-coded list of 18 or 19 module directories, then `go build -a` of `<item>/main.go`. The build info path is `command-line-arguments`. Both stamp `main.version`, `main.commit` and `main.date` from `VERSION`, `COMMIT` and `DATE`.
  - `FIPS=on` sets `GOFIPS140=v1.0.0` and `-tags fips140`, and a `RUN` checks `-tags=fips140,fips140v1.0`.
  - The builder stage is `golang:1.27-alpine@sha256:4c9f…` with `--platform=$BUILDPLATFORM`, so it cross-compiles and needs no QEMU. The runtime image is `gcr.io/distroless/static:nonroot@sha256:e2e9…`.
  - Runtime image config: `WORKDIR /`, `USER 65532:65532`, `ENTRYPOINT ["/flagd-build"]` (`["/flagd-proxy"]` for flagd-proxy), no `CMD`, no `EXPOSE`, no `LABEL`, no shell.
  - Images are released for `linux/amd64,linux/arm64` (`release-please.yaml:116,135`). The Makefile's manual `docker-push-flagd` target also builds `linux/ppc64le,linux/s390x`.
  - Out of scope: `flagd/profile.Dockerfile` (pprof) and the root `Dockerfile` (mkdocs).
- **The Builder** generates the source and builds a host binary with `-X main.version/commit/date` (`builder/internal/build/build.go`). It does no cross-compile, no FIPS build and no image, and ships no Dockerfile.
- **Release workflow** (`.github/workflows/release-please.yaml`):
  - It is disabled in the repository settings and guarded by `if: github.repository == 'open-feature/flagd'`.
  - `PUBLISHABLE_ITEMS` is `["flagd","flagd-proxy"]`.
  - The container job pushes `ghcr.io/<owner>/<item>:latest|v<ver>|latest-fips|v<ver>-fips`, with `provenance: mode=max` and `sbom: true`, and cosign-signs both digests.
  - Labels come from `docker/metadata-action`, not from the Dockerfile.
  - The binaries job calls `.github/scripts/release-build.sh <item> …`: 6 standard and 5 FIPS archives, each holding the binary, LICENSE, CHANGELOG.md, README.md and sbom.xml.
- **`release-please-config.json`:** packages `flagd`, `flagd-proxy` and `core`, `include-component-in-tag`, separator `/`. No component module is registered.
  - `core/go.mod` is now `R/core`, while the existing `core/v0.19.x` tags were cut for `github.com/open-feature/flagd/core`.
  - `flagd/go.mod` stays `github.com/open-feature/flagd/flagd`.
- **`release-dry-run.sh`** (job `release-dry-run`, gated by `FULL_CI` or a manual run) builds both items through `release-build.sh` and checks:
  - archive names and contents, build info path, FIPS tags and the version line;
  - by grep, the workflow's literal image names, tag suffixes, owner and guard;
  - the flagd image only: entrypoint, user and version line.
- **CI (`build.yaml`).**
  - Ungated: `lint`, `test` (includes `test-distributions`), `builder-e2e`, `docs-check`, `integration-test`, and `benchmark` (allocs/op only, merge commit against `pull_request.base.sha`).
    - `integration-test` builds `flagd/build.Dockerfile`, which is flagd-full since UC-009, and runs the RPC and in-process tags held in `test/integration/integration_test.go:173,207`.
  - `upgrade-test` runs on Dockerfile or build path changes (its gate step, lines 285–314), or with `FULL_CI`.
  - Gated behind `FULL_CI` or a manual run: `fips`, `docker-local` and `release-dry-run`.
  - `paths-ignore: docs/**, README.md` is UC-013 A1 today.
  - `compare-allocs.py` keys each benchmark by `pkg` path and name (lines 32–37).
- **`test/distributions`** builds every manifest with its `replaces`, that is, the modules of the change (UC-013 BR-002). Step 10 D2 adds `TestStandardModuleGraphs` (UC-013 step 4), D4 adds the FIPS compile per manifest to the gated job, and P3 adds `TestProxyRejectsWhatFlagdFullRejects`.

## 0. Open questions for the project lead

The recommendation comes first.

- **Q1, slicing and parallelism.**
  - **Recommendation:** a characterization PR plus three stacks. Section 7 has the branches.
    - **Runs in parallel now:** K (characterization), U (UC-002, on the existing test manifests `example` and `minimal-shape`), V1 (ns/op report), V2 (tag-selection pin) and R4 (module paths).
    - **After step 10's D:** V3 (FIPS closures).
    - **After D and U1:** R1–R3 (UC-012 dry run).
    - **With P3:** V4 (Q15, an extension of P3's test).
    - **Last:** the closing PRs.
    - The critical path is D → R → X-012.
  - *Alternative:* wait for all of step 10, then one stack per use case in sequence.

- **Q2, flagd-full's image.**
  - **Recommendation:** `flagd/build.Dockerfile` and `flagd-proxy/build.Dockerfile` stay byte-identical, and flagd-full keeps being built from them. The reference Dockerfile builds the other standard distributions and custom distributions. BR-002 holds for flagd-full through `check-flagd-full-modules`.
  - *Alternative:* build flagd-full from the reference Dockerfile. The build info path and the cache layout change; that is an item of #78.

- **Q3, where the reference Dockerfile lives (UC-002 step 1).**
  - **Recommendation:** `builder/reference/Dockerfile`, a static file documented in `docs/relay/how-its-built.md`. The Builder does not emit it.
  - *Alternative:* `relay-builder --dockerfile` writes it next to the generated source.

- **Q4, the entrypoint of the reference image.** `ENTRYPOINT` in exec form cannot expand an `ARG`.
  - **Recommendation:** `/flagd-build`, as in today's flagd image. A Deployment that sets `command:` keeps working when it swaps `ghcr.io/open-feature/flagd` for a standard distribution image.
  - *Alternative:* a neutral `/distribution`.

- **Q5, build context and `replaces` (UC-002 steps 3–4).**
  - **Recommendation:** the reference Dockerfile builds `./${DIST_PATH}` with `GOWORK=off`. `DIST_PATH` defaults to `.`.
    - **A custom distribution with released modules:** the context is its source directory.
    - **A repository manifest:** the context is the repository root and `DIST_PATH` is the generated directory, so its `replaces` stay inside the context.
    - **Build flags and pins:** the same as flagd's, and the same two pinned digests (BR-005).
  - *Alternative:* `go mod vendor` before the build.

- **Q6, proving publish and pull (UC-002 steps 6–9) without pushing.**
  - **Recommendation:** buildx writes the multi-platform image as an OCI layout (`--output type=oci`). The test checks that its index lists every target platform, then loads the local platform and runs it.
    - **Accepted gaps:** steps 7–8, A5 and A6 are the registry's and Docker's behaviour.
  - *Alternative:* an ephemeral `registry:2` on 127.0.0.1, push and pull through it.

- **Q7, image identity (UC-002 postcondition 3).**
  - **Recommendation:** no `LABEL` in the reference Dockerfile, like today's. The image reports its identity through `version --verbose`.
  - *Alternative:* OCI labels from the same build args, in the reference Dockerfile only.

- **Q8, the release workflow and how much the dry run builds (UC-012 steps 5–10). (changed in revision 2)**
  - **Recommendation:** `release-please.yaml` stays byte-identical (K2), so flagd and flagd-proxy release exactly as today. The dry run proves the standard distributions' release on the smallest set.
    - **flagd-minimal, the full path:** 11 archives, standard and FIPS images for `linux/amd64` and `linux/arm64` as OCI layouts with attestations, the FIPS refusal, and `version --verbose`.
    - **flagd-cloud, flagd-kubernetes and flagd-sync, only what differs:**
      - the archive and image names BR-012 yields;
      - one `linux/amd64` standard compile through `release-build.sh`, with its archive name and contents;
      - the rendered manifest (Q10).
    - **flagd and flagd-proxy:** as today.
    - **Accepted gaps, owned by "after the incubator":**
      - every standard distribution through the full platform matrix and both variants;
      - wiring into the workflow, tagging and signing;
      - steps 1–2, 4–6, A1, A3 and A6, which are release-please's and the registry's behaviour.
  - *Alternative:* the full path for all five distributions. That is 55 archives and 10 multi-platform images, and probably a matrix job.

- **Q9, registering component modules with release-please (UC-012 precondition, FR-031). (changed in revision 2)**
  - **Recommendation:** no change to `release-please-config.json` or `.release-please-manifest.json`. The day release-please runs, every registered package would open a release and tag at once. That is a release decision for after the incubator.
    - **R4 proves only the tag form:** each component module's path is `R/<dir>`, so `<dir>/vX.Y.Z` resolves (BR-001, A4).
    - **FR-031** moves to `In Progress`, with "registration with release-please" as the named gap.
  - *Alternative:* register about 15 packages now, inert while the workflow is guarded and disabled.

- **Q10, published manifests (UC-012 BR-004, BR-005, A2). (changed in revision 2)**
  - **Recommendation:** the dry run renders each standard distribution's published manifest: every module at the one documented placeholder `v0.0.0-dryrun`, and no `replaces`. It checks statically that there are no replaces, and that every module the manifest names is a component module directory of this repository; one that is not fails, naming it (A2's shape).
    - **The builds use the tree.**
    - **Accepted gap:** building against real released versions (BR-005, step 6).
  - *Alternative:* a file `GOPROXY` assembled from the tree with `golang.org/x/mod/zip`, with the rendered manifests built through it (about 150 lines).

- **Q11, signing and attestations (UC-012 BR-009).**
  - **Recommendation:** flagd-minimal's images are built with `--provenance=mode=max --sbom=true` into the OCI layout, and the dry run checks both attestation manifests. Archive SBOMs are checked as today. cosign signing is an accepted gap.
  - *Alternative:* keyless `cosign sign` against a local registry (Q6's alternative).

- **Q12, FIPS builds on every pull request (UC-013 BR-001, step 3, A3).**
  - **Recommendation:** keep the FIPS job gated, as `CLAUDE.md` decides. D4 already puts the FIPS compile of every manifest there, and V3 adds the crypto-closure subset check. "Ungated on every PR" is an accepted gap until the PoC ends.
  - *Alternative:* move only the five FIPS compiles into the ungated `test-distributions`.

- **Q13, "required for merging" (UC-013 precondition).**
  - **Recommendation:** no change to repository settings. `docs/relay/status.md` lists the checks UC-013 maps to, and branch protection is an accepted gap for the project lead.
  - *Alternative:* the project lead marks the ungated jobs as required now.

- **Q14, the benchmark baseline (UC-013 BR-005, NFR-001). (changed in revision 2)**
  - **The facts:** CI compares the merge commit with `pull_request.base.sha`. BR-005 says "merge base". NFR-001 says "the upstream commit the work branched from", which is the performance half of the drop-in promise.
  - **Recommendation:**
    - **Amend only UC-013 BR-005** to "the pull request's base commit". NFR-001's wording stays.
    - **NFR-001 against upstream b23f82ec is an accepted gap**, owned by after the incubator, and NFR-001 stays `Open`.
    - **Why no check now:** `compare-allocs.py` keys benchmarks by package path. Every path moved from `github.com/open-feature/flagd/…` to `R/…`, so a comparison with b23f82ec finds no common benchmark without a mapping from old paths to new ones.
  - *Alternative:* that mapping in `compare-allocs.py`, plus a step in `upgrade-test`, which already fetches the full history, running both sides once.

- **Q15, the FR-026 set (UC-013 precondition, step 6, BR-008). (changed in revision 2)**
  - **Recommendation:** `test/distributions/testdata/flag-configurations/{valid,invalid}`, seeded from P3's invalid contract samples, the test-harness's invalid flag files, and the valid test-harness and sample flag files.
    - **Verdicts:** a file's verdict is flagd-full's (BR-008).
    - **One test:** P3's `TestProxyRejectsWhatFlagdFullRejects` reads this set and checks both directions. A mismatch lists the file and both verdicts (A8).
  - *Alternative:* P3's rejection test only, and BR-008's "accepts every one" amended.

- **Q16, where the image tests and the dry run run. (changed in revision 2)**
  - **Recommendation:**
    - **`test/image` runs in the `upgrade-test` job** as a second `make` target after the differential. The job's change pattern gains `^builder/reference/` and `^test/image/`. That keeps one job, one gate and one set of cached layers.
    - **`release-dry-run` gets the same gate step,** with the pattern `^\.github/scripts/release-|^builder/reference/|^distributions/|Dockerfile$`. It runs when its inputs change, as well as with `FULL_CI` or a manual run.
  - *Alternative:* both stay gated by `FULL_CI` only.

- **Q17, spec corrections, folded into the closing PRs. (trimmed in revision 2)**
  - Glossary: add Image Build Environment (UC-002's actor), and FIPS Build if the closing PRs' text uses it. Add the actor to `use_cases.puml`.
  - UC-013 BR-005 (Q14).
  - The accepted gaps of Q6, Q8–Q11, Q12 and Q13 in their use cases.
  - Statuses: FR-009, FR-030, FR-031 (`In Progress`), FR-032, NFR-009 and C-009 as delivered.

## 1. Scope

**Realizes:**

| Requirement | Delivered by |
|---|---|
| FR-009, UC-002 BR-001–BR-007 | U |
| FR-030 (dry run), NFR-009 and C-009 (on flagd-minimal), UC-012 BR-003, BR-004, BR-006–BR-009, BR-012, BR-013 | R (Q8) |
| FR-031 (tag form only; `In Progress`) | R4 (Q9) |
| FR-032, FR-026 (CI half), UC-013 A10 | V, plus what CI and step 10 already run |
| C-003, C-011 | K (images, release and release configuration unchanged) |

**Use case coverage:**

| Flow | Test |
|---|---|
| UC-002 main 2–6, BR-001 | `test/image` `TestReferenceImageMultiPlatform`: the OCI index lists `linux/amd64` and `linux/arm64` |
| UC-002 main 9, A2, BR-003, postcondition 3 | `TestReferenceImageRuns`: `--load`, user `65532:65532`, entrypoint `/flagd-build` (Q4), no `/bin/sh`, and `version --verbose` prints the name, version, commit and date given |
| UC-002 BR-002, postcondition 2 | `TestImageSameComponentsAsBinary`: `go version -m` of the binary copied out of the image equals the Builder-built binary's module lines |
| UC-002 A1 | a `TestReferenceImageMultiPlatform` row adding `linux/s390x` beyond the default |
| UC-002 A3, BR-007 | `TestReferenceImageFIPS`: tags `fips140,fips140v1.0`, and `version` says `build variant: fips` |
| UC-002 A4, BR-004 | `TestPlatformCompileFailureBuildsNoImage`: a fixture component that fails only on arm64; the build exits non-zero and names `linux/arm64`, and the OCI destination is empty |
| UC-002 BR-005, BR-006 | `TestReferenceDockerfilePins` (no Docker): digest-pinned `FROM` lines equal to `flagd/build.Dockerfile`'s; no `ghcr.io/open-feature` in the file |
| UC-002 main 7–8, A5, A6 | accepted gap / no system behaviour (Q6) |
| UC-012 main 7, 9, BR-006, BR-012 | dry run: flagd-minimal's 11 archives; one `linux/amd64` archive each for the other three, with BR-012's names and the five-entry contents |
| UC-012 main 8, BR-007 | dry run: flagd-minimal's FIPS tags per archive, and `GODEBUG=fips140=off` on its `linux/amd64` FIPS binary exits non-zero |
| UC-012 main 10, BR-009 | dry run: flagd-minimal's standard and FIPS OCI images for both platforms, the BR-012 image names and tags for all four, attestation manifests present (Q11) |
| UC-012 BR-003 | dry run: `version --verbose` of each built binary shows `v<flagd version>` |
| UC-012 BR-004, A2 | dry run: the rendered manifests (Q10) |
| UC-012 BR-008, A5 | dry run: everything is staged in one temporary directory and the script exits non-zero before its summary on any failure; the workflow is a gap (Q8) |
| UC-012 A4, BR-001 | `test/release` `TestModulePathsMatchTagDirectories` |
| UC-012 BR-013 | K1–K3: flagd-proxy's Dockerfile, image configuration and workflow entries unchanged |
| UC-012 main 1–6, A1, A3, A6, BR-002, BR-010, BR-011, the full matrix for all five | accepted gap (Q8, Q9) |
| UC-013 main 2, A2 | existing `lint` and `test` |
| UC-013 main 3, BR-001, BR-002, A3 | `test-distributions` (step 10 D), the FIPS compile per manifest (D4, gated, Q12), V3 |
| UC-013 main 4, A4, BR-006 | step 10 D2 `TestStandardModuleGraphs`, `check-module-boundary`, `check-evaluator-boundary` |
| UC-013 main 5, BR-004, A5 | existing `integration-test` on the flagd-full image |
| UC-013 BR-003, A7 | V2 `TestTagSelectionMatchesBranchPoint` |
| UC-013 A6 | existing: the integration test fails with `failed to build flagd image` before any scenario; no new test (gap) |
| UC-013 main 6, A8, BR-008 | P3's `TestProxyRejectsWhatFlagdFullRejects` over Q15's set (V4) |
| UC-013 main 7, A9, BR-005 | existing `benchmark` job |
| UC-013 A10 | V1 ns/op report |
| UC-013 A1, BR-007 | K2 `test_workflows.py`: `paths-ignore` for docs, and no `push: true` or registry login in `build.yaml` |
| UC-013 precondition "required"; NFR-001 against upstream | gaps (Q13, Q14) |

**Leaves out:**

- any real tag, release, push or signature;
- any edit to `release-please.yaml`, `release-please-config.json` or `.release-please-manifest.json` (Q8, Q9);
- flagd-proxy's Dockerfile and release (BR-013);
- `profile.Dockerfile`;
- the full release matrix for flagd-cloud, flagd-kubernetes and flagd-sync (Q8);
- building against released component versions (Q10);
- NFR-001 against upstream (Q14);
- branch protection (Q13);
- UC-015 and UC-016.

## 2. Characterization (PR K, base `main`, first commit before any change)

Already pinned: the dry run's archive names and contents, build info path, FIPS tags, flagd version line, flagd image entrypoint and user, and the workflow greps; `check-flagd-full-modules`; the `fips` job's refusal and closure checks.

| Pin | Test | Pins |
|---|---|---|
| K1 | `release-dry-run.sh`, plus `.github/scripts/testdata/image-config.golden` | flagd's image, standard and FIPS: `docker image inspect` `.Config` reduced to `Entrypoint`, `Cmd`, `User`, `WorkingDir`, `ExposedPorts`, `Labels` and `Env`, equal to the golden (no ports, no labels). No `/bin/sh`. Both platforms in a `--output type=oci` build |
| K1b | one run in K's PR, recorded as `.github/scripts/testdata/flagd-proxy-image-config.golden` | flagd-proxy's image configuration, the same fields. It is not rebuilt on every dry run; K3's Dockerfile hash keeps the golden valid, and a hash change means re-recording it |
| K2 | `.github/scripts/test_workflows.py`, run in `lint` beside `test_compare_allocs.py` | sha256 of `release-please.yaml`, `release-please-config.json` and `.release-please-manifest.json` equal recorded values (Q8, Q9); `build.yaml`'s `paths-ignore` (UC-013 A1), no `push: true` and no `docker/login-action` (UC-013 BR-007), and the gated job names |
| K3 | `test_workflows.py` | sha256 of `flagd/build.Dockerfile` and `flagd-proxy/build.Dockerfile` recorded (Q2) |
| K4 | `test/upgrade` (existing) | flagd-full's image against upstream b23f82ec stays green through the step; no new `accepted.yaml` entry is expected |

## 3. Steps

Every commit builds and passes `make test`. No step edits the files K2 and K3 hash.

### Stack U (UC-002; base `main`, parallel now)

| Step | Model | Files | Change | Check |
|---|---|---|---|---|
| U1 | implementer | `builder/reference/Dockerfile`, `docs/relay/how-its-built.md` | Q3–Q5 and Q7: `ARG DIST_PATH=. VERSION COMMIT DATE FIPS=off`, `GOWORK=off go mod download` with a cache mount, `go build -a` of `./${DIST_PATH}`, the FIPS check, distroless `nonroot`, `/flagd-build`, `USER 65532:65532`; flagd's two digests | `TestReferenceDockerfilePins` |
| U2 | implementer | new module `test/image` (`IMAGE_TEST=1` gate like `UPGRADE_TEST`), fixture `test/image/fixtures/arm64-broken`, `Makefile` `image-test` | Section 1's UC-002 rows on `test/distributions/example` and `minimal-shape` | all green locally in WSL with Docker |
| U3 | mechanic | `.github/workflows/build.yaml` | `upgrade-test`: pattern gains `^builder/reference/` and `^test/image/`; `make image-test` after `make upgrade-test` (Q16) | the job runs both on U's PR |

### Stack V (UC-013)

| Step | Model | Base | Files | Change | Check |
|---|---|---|---|---|---|
| V1 | implementer | `main` (now) | `.github/scripts/compare-allocs.py`, `test_compare_allocs.py` | Report every benchmark whose ns/op differs from the base by more than 5% while its allocs/op did not rise, as a `::notice::` and a step-summary table; never fails (A10) | new cases in `test_compare_allocs.py`; the exit status is unchanged on the existing fixtures |
| V2 | implementer | `main` (now) | `test/integration/tag_selection_test.go`, `testdata/tag-selection.golden` | The RPC and in-process tag expressions from `integration_test.go`, equal to the golden recorded from b23f82ec. A change fails with both expressions printed; the review accepts it by updating the golden with a reason (A7) | test green; a mutated tag fails |
| V3 | mechanic | step 10 D | `Makefile` (`fips-closure-standard`), `build.yaml` `fips` job | Each standard distribution's FIPS crypto closure is a subset of flagd-full's (A3) | gated FIPS job green on a manual run |
| V4 | implementer | step 10 P3 (a commit on P3's branch, or stacked on it) | `test/distributions/testdata/flag-configurations/{valid,invalid}`, P3's `TestProxyRejectsWhatFlagdFullRejects` | Q15: the test reads the set and checks both directions; a mismatch lists the file and both verdicts (A8) | green; moving one valid file into `invalid/` fails naming it |

### Stack R (UC-012)

| Step | Model | Base | Files | Change | Check |
|---|---|---|---|---|---|
| R1 | implementer | step 10 D + U1 | `.github/scripts/release-build.sh` | Optional sixth argument, the source directory, defaulting to `./<item>`; optional `RELEASE_PLATFORMS` (default: today's list). A non-default source directory builds `./` of it with `GOWORK=off` and takes CHANGELOG.md from `flagd/` (BR-003). Called as today, the output is identical | K1 green; flagd's and flagd-proxy's archive listings unchanged against the dry run before R1 |
| R2 | implementer | R1 | `.github/scripts/release-dry-run.sh`, `build.yaml` (`release-dry-run` gate step, Q16) | Q8: the four distributions are generated with `relay-builder --skip-compile` into `distributions/<x>/build`. flagd-minimal takes the full path, with images from `builder/reference/Dockerfile` (Q11). The other three get one `linux/amd64` archive and their BR-012 names | dry run green on R's PR (its gate fires); the summary lists 11 + 3 archives for the new distributions and 2 OCI images |
| R3 | implementer | R2 | `.github/scripts/render-published-manifest.sh`, `test/release` | Q10: rendered manifests at `v0.0.0-dryrun` in the staging directory; no `replaces`; a module outside this repository's component directories fails, naming it | `test/release` `TestRenderedManifestHasNoReplaces`, with a fixture naming an unknown module failing |
| R4 | implementer | `main` (now) | new module `test/release` | `TestModulePathsMatchTagDirectories` (Q9) | `make test` |

### Closing PRs

| PR | Model | Content |
|---|---|---|
| X-002 | mechanic | UC-002 `Implemented`, with steps 7–8, A5 and A6 as gaps (Q6); FR-009; Image Build Environment in the glossary and `use_cases.puml` (Q17) |
| X-012 | mechanic | UC-012 `Implemented` (dry run), gaps per Q8–Q11; FR-030, NFR-009, C-009 statuses; FR-031 `In Progress`; `docs/relay/distributions.md` names and archives |
| X-013 | mechanic | UC-013 `Implemented`; BR-005's wording (Q14); gaps Q12, Q13, Q14 and A6; `docs/relay/status.md` check list; FR-032 status |

## 4. Gates

In WSL from the repository root (`export GOTOOLCHAIN=go1.27.3`; in a worktree `export GOWORK=$PWD/go.work`; for lint `export GOPATH=$(go env GOPATH)`). CI runs everything except spec lint and ns/op; the Docker jobs run when their change pattern matches, with `FULL_CI`, or on a manual run.

| Gate | Command | Slices |
|---|---|---|
| Unit tests | `make workspace-init && make test` | all |
| Lint | `make lint`; `python3 .github/scripts/test_workflows.py`, `test_compare_allocs.py` | all; K2, V1 |
| Boundaries | `make check-module-boundary check-evaluator-boundary` | all |
| Image tests | `make image-test` (`IMAGE_TEST=1`, Docker with buildx), in CI inside `upgrade-test` | U, R |
| Release dry run | `.github/scripts/release-dry-run.sh`; in CI the `release-dry-run` job behind its gate step | K, R |
| FIPS | gated `fips` job on a manual run | V3 |
| Upgrade differential | CI's `upgrade-test` (fires on K, U and R) | K, U, R |
| Acceptance | `make flagd-integration-test` | V2 |
| Dependencies | the two `golang.org/x/exp` greps | all |
| Spec | `spec_lint.py --docs docs` on Windows | closing PRs |

## 5. Risks

- **R1, the real release path is unproven.** The workflow and the release configuration stay untouched (Q8, Q9), nothing is tagged (Q10), and nothing is signed (Q11). UC-012 lands with the longest gap list of the PoC. The dry run proves what the scripts build on one standard distribution, not that release-please, tags, pushes and signatures work for five. Q10's alternative is the cheapest lever, since it proves BR-005 without tags.
- **R2, core's module path against its tags.** The existing `core/v0.19.x` tags belong to `github.com/open-feature/flagd/core`, while `core/go.mod` is `R/core`. R4 records that the next `core/v…` tag resolves only from this repository's path; the path itself is not changed here.
- **R3, NFR-001 against upstream (Q14).** Until the path mapping exists, an allocs/op drift that no single PR shows is not compared with today's flagd. The gap is named in X-013, and NFR-001 stays `Open`.
- **R4, digest drift.** The reference Dockerfile repeats flagd's two digests. `TestReferenceDockerfilePins` fails when one is bumped without the other.
- **R5, the entrypoint name (Q4).** `/flagd-build` in a custom image is odd, but swapping images stays drop-in. The docs say why.
- **R6, buildx in tests.** The OCI exporter and attestations need the `docker-container` driver. `test/image` creates its own builder and removes it.
- **R7, `upgrade-test` job time.** It gains `make image-test`, about 4 single-distribution image builds. Its 75-minute timeout stays; measure on U's PR.
- **C-003 surface.** No change to the flagd or flagd-proxy image, archive, workflow, release configuration or CLI. K1–K4 pin this, and no `accepted.yaml` entry is expected. **C-005:** no protobuf change. **NFR-005, NFR-006:** `test/image` and `test/release` are test modules with boundary rows like `test/distributions`.

## 6. Hand-overs this step closes

| From | Gap | Closed by |
|---|---|---|
| UC-010 | A1, BR-005: images and FIPS images of the standard distributions | U, R2 (flagd-minimal in full; the others by name, Q8) |
| step 10 Q17 | Images, archives and release names | R2 |
| UC-009 | A1's archive names match UC-012 BR-012 | R2 (flagd-full's names unchanged) |
| step 10 Q20 alternative | flagd-proxy's separate release | K1b, K2, K3 (unchanged) |

## 7. PR split

| PR | Branch | Base | Content | Runs |
|---|---|---|---|---|
| K | `step-12/pr-1-characterization` | `main` | K1–K4 | now |
| U | `uc-002/pr-1-reference-image` | `main` | U1–U3 | now |
| V1 | `uc-013/pr-1-ns-report` | `main` | V1 | now |
| V2 | `uc-013/pr-2-tag-selection` | `main` | V2 | now |
| R4 | `uc-012/pr-1-module-paths` | `main` | R4 | now |
| V3 | `uc-013/pr-3-fips-closures` | step 10 D | V3 | after D |
| V4 | `uc-013/pr-4-verdict-set` | step 10 P3 | V4 | with P3 |
| R | `uc-012/pr-2-release-dry-run` | step 10 D + U | R1–R3 | after D and U1 |
| X-002, X-012, X-013 | `uc-0xx/pr-n-close` | their last PR | status and spec | last |

Every PR description starts with `Part of #14`, `#24` or `#25`; K names all three.

## 8. Changelog (revision 2, `step-12-opposer.md`)

| Finding | Severity | Outcome |
|---|---|---|
| 1 R2 is not the smallest proof | major | Resolved. Q8: flagd-minimal takes the full path; the other three are checked on names, one `linux/amd64` archive and the rendered manifest. The full matrix for all five is a named gap. The old CI-time risk is gone |
| 2 Q14 rewrites NFR-001 | major | Resolved. Only UC-013 BR-005 is amended, and NFR-001 keeps its wording. NFR-001 against b23f82ec is a named gap (R3), not a check: `compare-allocs.py` keys by package path and every path moved, so the opposer's one-line check would compare nothing. The mapping is Q14's alternative |
| 3 Q9 changes the release configuration | major | Resolved. Q9 takes the alternative: no configuration change, and K2 also hashes both release-please files. R4 keeps the tag-form test; FR-031 moves to `In Progress`; Q10 uses the placeholder `v0.0.0-dryrun` |
| 4 a new CI job for image tests | major | Resolved. Q16 and U3: `test/image` runs inside `upgrade-test`, which gains the two patterns |
| 5 A1's platforms are not today's | minor | Resolved. The row adds `linux/s390x` beyond the default, without the "today's" claim. "What exists" says which platforms the release builds (amd64, arm64) and which the Makefile's `docker-push-flagd` builds |
| 6 V4 repeats P3 | minor | Resolved. V4 extends P3's `TestProxyRejectsWhatFlagdFullRejects` with Q15's set |
| 7 the dry run rarely runs | minor | Resolved. Q16: `release-dry-run` gets `upgrade-test`'s gate step with its own input pattern |
| 8 K1 builds flagd-proxy every run | minor | Resolved. K1 covers flagd's image only. K1b records flagd-proxy's configuration once, kept valid by K3's Dockerfile hash |
| 9 Q17's glossary additions | minor | Resolved. Only Image Build Environment, plus FIPS Build if the closing text uses it |
