# Step 17 plan: publish the standard distributions and flagd-testbed to ghcr from `main` (issue #216)

**Status: revision 1, for the opposer. Four questions are open for the project lead (section 0).**

**Decided by the project lead (#216):**
- **Images:** flagd-full, flagd-minimal, flagd-minimal-otel, flagd-cloud, flagd-kubernetes and flagd-sync, each in standard and FIPS, plus flagd-testbed. All are linux/amd64 + linux/arm64, under `ghcr.io/open-feature-forking/`.
- **Not pushed:** flagd and flagd-proxy (they would be confused with the official images), and the Flagsmith distribution.
- **Tags:** `:main`, which moves, and `:sha-<short>`, which is immutable. No `latest`, no semver.
- **Push only from `main`.** This lifts the standing "never push images" rule deliberately; release-please stays disabled.

**Base:** `main` @ b26e0665. "R" is `github.com/open-feature-forking/openfeature-relay`.

**Read:** `release-dry-run.sh`, `release-dry-run-distributions.sh`, `builder/reference/Dockerfile`, `distributions/flagd-testbed/Dockerfile`, `build.yaml` (`release-dry-run`, `testbed`), `release-check.yaml`, `test_workflows.py` (K2, K3), UC-002, UC-009, UC-012 BR-012, `docs/relay/try-it.md`.

## 1. What exists, and what is missing

| Image | Built today | Where | Gap |
|---|---|---|---|
| flagd-full | as `flagd`, from `flagd/build.Dockerfile` | `images` part | It is built under flagd's name (BR-012) and from flagd's Dockerfile. Under its own name it needs the reference Dockerfile with `distributions/flagd-full/build`. |
| flagd-minimal, standard and FIPS | multi-arch OCI layout with provenance and SBOM | `distributions` part | Only the push is missing. |
| minimal-otel, cloud, kubernetes, sync | **no image**, one linux/amd64 archive each | `distributions` part | Both images, multi-arch, are missing. |
| flagd-testbed | linux/amd64 `docker build`, loaded | `testbed` job, `make testbed-*` | Multi-arch is missing. The `certs` stage (`alpine/openssl`, `RUN openssl`) and `RUN mkdir flags` in the `busybox` stage run on the target platform, so arm64 needs QEMU today. |

**Facts the plan relies on:**
- **`build.yaml` has no `permissions` block.** Every job gets the repository's default token. `release-check.yaml` already sets `contents: read`.
- **The dry run stamps version `9.9.9`.** Pushed as it is, `version` would print `v9.9.9`, which looks like a release (Q4).
- **The dry run tags its OCI layouts `ghcr.io/open-feature/<name>:latest` and `:v<version>`, and checks those tags.** The push goes by digest, so those tags never leave the runner.
- **"Never push images" is not written in CLAUDE.md.** The rule lived in the sessions' containment notes, and UC-002's gap table says "Nothing is pushed during the proof of concept". CLAUDE.md therefore gains the new rule (step D) instead of losing an old one.
- **The 2-platform build adds no QEMU.** Both Dockerfiles cross-compile with `$BUILDPLATFORM`. Only the testbed needs a change to keep that true.

## 0. Questions for the project lead

- **Q1, build once, push from the layouts. (open)**
  - (a) **Build in the unprivileged dry run, push in a separate job.** The dry run builds every image as an OCI layout and uploads them as artifacts (`retention-days: 1`). A new `publish-images` job (`needs: [release-dry-run, testbed]`) downloads them and pushes them by digest with `crane`/`skopeo copy --all --preserve-digests`. It holds `packages: write` and runs no repository build code. On `main`, two new matrix parts keep the wall time at ~11 min (estimate, B measures): `distribution-images` (the four missing distributions and flagd-full under its own name) and `testbed-image`.
  - (b) A push job that rebuilds with `docker/build-push-action` and a `type=gha` cache shared with the dry run. It is simpler, but it is a second build that hits the cache only when the build args are identical, and its `go build -a` layer misses after every change to `go.sum`.
  - (c) The dry run itself pushes on `main`. It is the least YAML, but `packages: write` then sits in the job that runs the whole build and its downloaded modules, and the "dry run" is no longer dry.
  - **Recommended:** (a). The digest the dry run checked is the digest that is pushed, and the token never meets the build.

- **Q2, signing and provenance. (open)**
  - (a) **GitHub artifact attestations.** Use `actions/attest-build-provenance` on each pushed digest (`push-to-registry: true`, `id-token: write` and `attestations: write` on `publish-images` only), plus buildx's provenance and SBOM that the layouts already carry. Users verify with `gh attestation verify oci://… -R open-feature-forking/openfeature-relay`.
  - (b) cosign keyless (`cosign sign` with the workflow's OIDC identity). Users verify with `cosign verify --certificate-identity …`. It needs one more pinned action.
  - (c) Neither. Only buildx's in-index provenance and SBOM; nothing is signed.
  - **Recommended:** (a). It needs no new tool for users who have `gh`. Both (a) and (b) write an entry to the public Sigstore transparency log that names this repository and workflow. The images are public anyway (Q3), but that is a record outside the repository, so it is the lead's call.

- **Q3, visibility, the link to this repository, and `sha-` retention. (open)**
  - (a) **Public, linked, and nothing deleted during the PoC.** Every image carries `org.opencontainers.image.source=https://github.com/open-feature-forking/openfeature-relay`, so ghcr links the 7 packages (FIPS is a tag suffix, see section 2) to this repository. After the first push an org owner sets each package to public once, and step D's check confirms it anonymously. `sha-` tags stay: public storage is free, and ~20 pushes a day that change code make ~280 tags a day across the packages.
  - (b) As (a), plus a weekly `cleanup-images` workflow (a pinned `actions/delete-package-versions`) that keeps the last 30 days of `sha-` tags and whatever `:main` points at. It means a second job with `packages: write`, on a schedule.
  - (c) Private packages, readable only by org members. That is not what #216 asks for: nobody outside could pull them.
  - **Recommended:** (a), with (b) opened as an issue if the version lists become a nuisance.

- **Q4, the stamped version and the "proof of concept" marking. (open)**
  - (a) **Version `0.0.0-main.<short>` with labels and annotations on every image.** On a `main` push, the dry run passes version `0.0.0-main.<short sha>` instead of `9.9.9`, so `version` prints `v0.0.0-main.abc1234`: a semver pre-release that sorts below every release. Each image carries labels and `index:`/`manifest:` annotations:
    - `org.opencontainers.image.title`: the image name;
    - `org.opencontainers.image.description`: "Proof of concept from the OpenFeature Relay incubator, built from main. Not an official OpenFeature image; not for production.";
    - `org.opencontainers.image.source`, `org.opencontainers.image.revision` (the full sha), `org.opencontainers.image.version` (`main-<short>`), `org.opencontainers.image.licenses=Apache-2.0`.

    No `vendor`, so nothing claims to be OpenFeature's.
  - (b) As (a), but the binary keeps `9.9.9`. It changes the least, but `version` looks like a release.
  - (c) Labels only on the pushed index, added by the push job with `crane mutate`. The checked digest is then no longer the pushed digest, which loses Q1 (a)'s point.
  - **Recommended:** (a). The labels go on through `--label`/`--annotation` in the dry-run scripts; no Dockerfile changes, so K3's hashes hold.

**Decided by the planner (defaults the lead can overrule):**
- **Names.** `ghcr.io/open-feature-forking/<name>`, with `<name>` = `flagd-full`, `flagd-minimal`, `flagd-minimal-otel`, `flagd-cloud`, `flagd-kubernetes`, `flagd-sync` or `flagd-testbed`.
  - flagd-full is published as `flagd-full`, not as BR-012's `flagd`, because #216 excludes `flagd`. This is a PoC deviation from BR-012, recorded in UC-012's gap list; a release keeps BR-012.
- **FIPS** is a tag suffix in the same package, as BR-012 does: `:main-fips` and `:sha-<short>-fips`. flagd-testbed has no FIPS variant.
- **Permissions.**
  - `build.yaml` gains a top-level `permissions: contents: read`.
  - `publish-images` adds `packages: write`, plus `id-token: write` and `attestations: write` under Q2 (a). No other job gets more.
  - `publish-images` runs only when `github.event_name == 'push' && github.ref == 'refs/heads/main' && github.repository == 'open-feature-forking/openfeature-relay'`, so a fork's `main`, a PR, `FULL_CI` and `workflow_dispatch` never push.
  - It logs in with `GITHUB_TOKEN` (`docker/login-action` pinned by SHA). `release-check.yaml` stays `contents: read`.
- **`:main` never moves backwards.** Runs on `main` are not cancelled (step 15's concurrency group), so two pushes can finish out of order. `publish-images` therefore has `concurrency: publish-images` (not cancelled). It always pushes `:sha-<short>`, and moves `:main` only when `git ls-remote origin refs/heads/main` still equals `github.sha`.
- **The testbed builds multi-arch without QEMU.** The `certs` stage runs `FROM --platform=$BUILDPLATFORM` (the certificates do not depend on the platform), and `RUN mkdir flags` becomes `WORKDIR /flags` followed by `WORKDIR /`. The final stage is then COPY-only, and its layout is unchanged; `make testbed-contract-both` is the check.
  - On `main`, the `testbed` job builds the 2-platform OCI layout and its local amd64 image with the same `buildx` builder, so the second build hits the cache.
- **K2 changes deliberately.** `test_nothing_is_pushed_or_logged_in` becomes `test_only_publish_images_pushes_or_logs_in`. It checks:
  - `docker/login-action`, `packages: write` and every registry push appear only in the `publish-images` job, with the exact `if:` above;
  - the top-level permissions are `contents: read`;
  - `release-check.yaml` has no `packages` key;
  - `release-please.yaml`'s hash is unchanged.

  The job list in `test_jobs` gains `publish-images`.
- **Docs.** `try-it.md`'s first line becomes "There are no releases. Images built from `main` are published for testing; they are a proof of concept, not official OpenFeature images." A new "Pull an image" section shows:
  - `docker run` of `flagd-minimal:main` with the sample flags mounted;
  - `flagd-testbed:main`;
  - `:sha-<short>` for a fixed build;
  - the Q2 verify command.

## 2. Spec change (for D)

- **UC-002:** the gap row "Steps 7–8, A5 and A6 … Nothing is pushed during the proof of concept" becomes "Steps 7–8 run on every push to `main` (`publish-images`) for the standard distributions and flagd-testbed under `ghcr.io/open-feature-forking/`; A5 and A6 remain the Container Registry's behaviour".
- **UC-012:**
  - the dry-run paragraph says images are built for all six standard distributions;
  - a new gap line covers the PoC names (flagd-full as `flagd-full`), the `:main`/`:sha-` tags in place of BR-012's, and no release.
  - BR-012 itself is unchanged.
- **No new FR or use case.** Publishing PoC images is CI, not Relay behaviour. `/spec-review` runs in D.

## 3. Tests

| Test | Where | Proves |
|---|---|---|
| `test_only_publish_images_pushes_or_logs_in` | `test_workflows.py` (K2) | the push surface, its `if:`, and the permissions (section 0) |
| `test_publish_images_needs_the_dry_run` | K2 | `needs: [release-dry-run, testbed]`, no build step in `publish-images`, `concurrency: publish-images` |
| `test_release_dry_run_parts` | K2, updated | the two new parts, and that `full` still covers today's archive and image lists |
| `BuildDockerfiles` | K3 | flagd's and flagd-proxy's Dockerfiles unchanged (only the testbed Dockerfile changes) |
| the dry run's OCI check, per image | `release-dry-run-distributions.sh` | name, `linux/amd64 linux/arm64`, 2 attestation manifests, provenance, SBOM, and the Q4 labels and annotations, for the six distributions × two variants |
| the publish rehearsal | `publish-images.sh` against a `registry:2` service on a `release-check` run and locally | the push by digest: the pushed index digest equals the layout's, the tags are `sha-<short>[-fips]` and `main[-fips]`, `:main` is skipped when the head moved, and the attestation manifests survive the copy |
| the first `main` push | the reviewer, after P merges | the 7 packages are public and linked; `crane manifest` shows both platforms; `docker run --rm ghcr.io/open-feature-forking/flagd-minimal:main version --verbose` prints `v0.0.0-main.<short>`; the Q2 verify passes; flagd and flagd-proxy are absent from the namespace |
| `make testbed-contract-both` | the `testbed` job | the testbed's layout is unchanged after the Dockerfile edit |

## 4. PR slicing

| PR | Model | Base | Content | Starts |
|---|---|---|---|---|
| B | implementer | `main` | The builds, with no push: the two new dry-run parts, flagd-full under its own name, the other four images, Q4's version and labels, the OCI layouts as artifacts on `main`, the testbed Dockerfile without QEMU and its 2-platform layout in the `testbed` job, and the dry-run checks of section 3. It measures each part's time on a `workflow_dispatch`. K2 gains only the part list | after the decisions |
| P | implementer | B | `publish-images.sh` and its rehearsal, the `publish-images` job, the permissions, the attestation (Q2), and K2's push test changed deliberately | after B exists (stacked) |
| D | mechanic | P, merged and its first `main` push checked | `try-it.md`, UC-002, UC-012, CLAUDE.md (below), `/spec-review`, #216 | last |

**CLAUDE.md in D:**
- Containment gains: "Images go only to `ghcr.io/open-feature-forking/`, only from `build.yaml`'s `publish-images` job on a push to `main`, and only the images #216 names. No session logs in to a registry or pushes an image by hand."
- The CI paragraph's line "Images are built only when…" adds "and on every push to `main`, which publishes them".

The critical path is B → P → the first `main` push → D. The org owner's visibility click (Q3) sits between P and D.

## 5. Gates

| Gate | Command | PRs |
|---|---|---|
| Workflow tests | `python3 .github/scripts/test_workflows.py` | B, P |
| Dry run, full | `workflow_dispatch` of `build` on the branch (it pushes nothing) | B |
| Dry run, reduced | the `release-check` label | B, P |
| Publish rehearsal | `publish-images.sh` against `registry:2` (in WSL, and on the labelled run) | P |
| Testbed | `make testbed-contract-both testbed-suites` | B |
| Image tests | `test/image` (CI) | B |
| First push | section 3's last-but-one row | after P |
| Spec | `spec_lint.py --docs docs` on Windows, and `/spec-review` | D |

## 6. Risks

- **R1, the token's reach.** `packages: write` on `GITHUB_TOKEN` can write every package this repository is linked to. Against that: one job; the `if:` with the repository check; no build code in the job, only the copy script, a pinned login and attest; K2 pins all of it. `pull_request_target` is not used anywhere.
- **R2, confusion with official images.** Different owner, no `latest`, no semver, a pre-release version string, the description, and no `flagd` or `flagd-proxy` package. A user can still pull `flagd-full:main` and run it in production. The description and `try-it.md` say not to; nothing enforces it.
- **R3, the queue.** Two more dry-run parts and one push job on every `main` push that touches code: 9 jobs against the 20-job org limit, on top of step 15's matrix. B measures; parts merge if the queue hurts.
- **R4, artifact size.** 13 multi-arch OCI layouts are an estimated 0.5–1 GB per `main` push, kept for one day. B measures. If it is too large, the push runs inside each part's job after all, which is Q1 (c)'s weaker position, and goes back to the lead.
- **R5, the testbed's certificate.** It is generated at build time, valid for 365 days, with its key inside the image, as upstream's testbed does. A `:sha-` tag older than a year serves an expired certificate. That is a test image; `try-it.md` says so.
- **R6, a red `main`.** `publish-images` needs the dry run and the testbed, so a red build publishes nothing and `:main` stays on the last green commit. That is the intent; the step summary names the reason.
- **R7, drop-in.** No change to flagd's image, name or Dockerfile (K3), or to anything an operator of today's flagd observes. The release path (`release-please.yaml`, `release-build.sh`) is untouched.

## 7. Considered

- **Pushing flagd-full as `flagd`** (BR-012) was rejected by #216.
- **A separate `publish.yaml` on `workflow_run`.** It would need the artifacts across workflows and a second checkout; `needs:` inside `build.yaml` is simpler and K2 already reads that file.
- **Semver tags for the PoC** were rejected by #216.
