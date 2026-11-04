# Step 12 plan: opposer findings (revision 1)

Plan: `.claude/plans/step-12.md` (revision 1, uncommitted). Checked against `origin/main`, `docs/use_cases/UC-002`, `UC-012`, `UC-013`, `docs/requirements.md` (FR-009, FR-030–FR-032, NFR-001, NFR-009, C-009, C-011), `.github/workflows/{build,release-please}.yaml`, `.github/scripts/`, and the project lead's scope rule: lean, dry runs only.

Counts: **0 blockers, 4 major, 5 minor.**

## What holds (verified)

- **The drop-in parts hold.** flagd's and flagd-proxy's Dockerfiles, `release-please.yaml` and the three existing `release-please-config.json` entries stay byte-identical, and K2 and K3 pin them. flagd-full keeps today's image path (Q2). The reference image's `/flagd-build` entrypoint (Q4) keeps an image swap drop-in. UC-012 BR-012 yields today's names for flagd-full.
- **The release workflow facts are right.** It is guarded by `if: github.repository == 'open-feature/flagd'` (`release-please.yaml:27`) and pushes images for `linux/amd64,linux/arm64` (lines 116 and 135). `release-dry-run` and `docker-local` are gated on `FULL_CI` or a manual run (`build.yaml:139-140`, `202-204`). `upgrade-test` has the changed-files gate step that Q16 reuses (`build.yaml:271-311`).
- **UC-002's rules map cleanly onto U:** BR-003 (no shell, non-root, binary as entry point), BR-005 (pinned digests, shared with flagd's Dockerfile), BR-006 (no `ghcr.io/open-feature` in the reference file), BR-004 (A4's arm64-only failure fixture).
- **UC-013's FIPS gap is the right call for the PoC.** BR-001 asks for FIPS builds on every pull request, and keeping the FIPS job gated (Q12) follows `CLAUDE.md`'s decision for the proof of concept.

## Major

### 1. R2 is not the "smallest proof" the scope rule asks for

- **Step:** Q8, R2, R3 (risk), section 1's UC-012 rows.
- **Evidence:**
  - R2 makes the dry run build every distribution "standard and FIPS, as archives and multi-platform images, with SBOMs and attestations". That is 5 × 11 archives and 5 × 2 OCI images, plus flagd-proxy.
  - Risk R3 already expects the gated job to exceed 45 minutes, and to need a matrix.
  - The scope rule says: "Each use case gets the smallest proof of its main success scenario. Everything else is a named accepted gap."
  - UC-012's main flow is proved by doing the full path once for one standard distribution besides flagd-full. For the others it is proved by the parts that differ: the name (BR-012), the manifest (BR-004) and the platform list. The build scripts are the same for every distribution.
- **Fix:**
  - Run the full path for **one** distribution, flagd-minimal: 11 archives, the standard and FIPS images for both platforms, attestations, the FIPS refusal and `version --verbose`.
  - For flagd-cloud, flagd-kubernetes and flagd-sync, check only what differs: the archive and image names BR-012 yields, a single `linux/amd64` compile through `release-build.sh`, and the rendered manifest.
  - Keep flagd and flagd-proxy as today.
  - Name "every distribution through every platform" as the gap that wiring the workflow closes.
  - That also removes R3.

### 2. Q14 rewrites NFR-001 and loses the comparison with today's flagd

- **Step:** Q14, X-013.
- **Evidence:**
  - NFR-001 compares against "the upstream commit the work branched from": today's flagd. It is the performance half of the drop-in promise.
  - CI's benchmark job compares each pull request with its base commit. For a stacked PR that is the parent branch, and after a merge it is the last `main`.
  - Amending NFR-001 to "the pull request's base commit" makes each step relative to the one before. An allocs/op drift that no single PR shows (for example +1 allocation over many PRs, with noisy benchmarks on the `NOISY` list) is then never compared with upstream again.
  - UC-013 BR-005's "merge base" can be amended to match CI without touching NFR-001.
- **Fix:**
  - Amend only UC-013 BR-005, to "the pull request's base commit".
  - Keep NFR-001's wording.
  - Either add one lean check, which runs the existing `run-benchmarks.sh` and `compare-allocs.py` once on `main` against `b23f82ec` (gated, like `upgrade-test`), or list "NFR-001 against upstream" as an accepted gap with an owner.

### 3. Q9 changes the release configuration, which is not a dry run

- **Step:** Q9, R4.
- **Evidence:**
  - Q9 adds one `release-please-config.json` package per component module, about 15, with an `initial-version` key the implementer still has to confirm.
  - The workflow is disabled today, but the configuration is what release-please acts on the day it runs. All those packages would then open release PRs and tag at once, with versions nobody has reviewed. That's a release decision for "after the incubator" (Q8's own gap owner), not something the PoC can prove.
  - The scope rule: "Nothing is released … Every image and release check is a dry run."
- **Fix:** take Q9's alternative. Keep `TestModulePathsMatchTagDirectories` (BR-001, A4), add no package entries, and move FR-031 to `In Progress` with "registration with release-please" as the named gap. Q10's rendered manifests then take every component's version from one documented placeholder (`v0.0.0-dryrun`), not from guessed initial versions.

### 4. A new CI job for the image tests

- **Step:** Q16, U3.
- **Evidence:** Q16 adds a job `image-test` with its own gate step, Docker and buildx. `upgrade-test` already runs on exactly the changes that matter (Dockerfiles, the builder, the build), with Docker available, and its gate step is what Q16 copies. A second job doubles the Docker setup, the image builds of the shared base layers, and the runner time.
- **Fix:** run `test/image` inside the `upgrade-test` job, as a second `make` target after the differential, and extend that job's change pattern with `^builder/reference/` and `^test/image/`. One job, one gate, one set of cached layers.

## Minor

### 5. The A1 row's platforms are not today's

- **Step:** section 1, the UC-002 A1 row (`linux/s390x,linux/ppc64le` "today's `docker-push-flagd` platforms").
- **Evidence:** `release-please.yaml:103`'s `platforms: all` is the QEMU setup step. The flagd images are built for `linux/amd64,linux/arm64` (lines 116 and 135), and UC-012 BR-006 says the same.
- **Fix:** test A1 with one platform beyond the default, for example `linux/s390x`, and drop the "today's" claim. Or drop the row, since A1 is the Distribution Builder's choice and BR-001's default is already tested.

### 6. V4 repeats step 10's P3

- **Step:** V4, Q15.
- **Evidence:** step 10's P3 already has `TestProxyRejectsWhatFlagdFullRejects` on the built flagd-sync and flagd-full. V4 adds a second test, `verdict_test.go`, that sends files to the same two binaries.
- **Fix:** extend P3's test with Q15's set, valid and invalid, in both directions (BR-008, A8), instead of a new test.

### 7. The dry run rarely runs

- **Step:** Q8, Q16, section 4 (Release dry run).
- **Evidence:** `release-dry-run` stays gated on `FULL_CI` or a manual run. After R lands, a change to `release-build.sh`, to a manifest or to the reference Dockerfile is not dry-run unless someone remembers to.
- **Fix:** give `release-dry-run` the same changed-files gate as `upgrade-test` (`.github/scripts/release-*`, `builder/reference/`, `distributions/`), so it runs when its inputs change. With finding 1's leaner scope, that run stays short.

### 8. K1 builds flagd-proxy images on every dry run

- **Step:** K1.
- **Evidence:** pinning flagd-proxy's image configuration (BR-013) is useful, but K1 builds both images, standard and FIPS, each time. Today the dry run doesn't build flagd-proxy's image at all.
- **Fix:** keep K1 for flagd's image. For flagd-proxy, pin the Dockerfile's sha256 (K3 already does) and its image configuration once, in K's PR, against a recorded golden, rather than on every dry run.

### 9. Q17's glossary additions

- **Step:** Q17.
- **Evidence:** six new glossary terms (Image Build Environment, Test Harness, FIPS Build, Platform Matrix, Release Tag, Release Pull Request) plus a new actor in `use_cases.puml`. With the lean scope, only the terms the delivered tests and docs use need adding. Spec lint already accepts the use cases as they are.
- **Fix:** add only Image Build Environment (UC-002's actor) and FIPS Build if the closing PRs' text uses them. Leave the rest to the use cases that deliver the release.
