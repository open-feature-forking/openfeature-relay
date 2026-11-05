# Step 17 plan: opposer findings (revision 1)

Plan: `.claude/plans/step-17.md` (revision 1, written against `main` @ b26e0665). Checked on `main` @ afa094e1.

Checked against the sources, not the plan's summary of them:
- on `main`: `.github/workflows/*` (all ten), `test_workflows.py` (K2, K3), `release-dry-run.sh`, `release-dry-run-distributions.sh`, `release-build.sh`, `builder/reference/Dockerfile`, `distributions/flagd-testbed/Dockerfile`, `flagd/build.Dockerfile`, the `Makefile`, `README.md`, `CLAUDE.md`;
- the repository's settings through the API: visibility, the default workflow token, branch protection and rulesets;
- `build` runs on `main`: the count per day (`gh run list -w build -b main -e push -L 400`) and the job times of run 42079716561 (afa094e1).

Counts: **1 blocker, 5 major, 8 minor.**

## What holds (verified)

- **The plan's facts:**
  - `build.yaml` has no top-level `permissions`; only `coverage` (`:287`) and `docker-local` (`:531`) set their own. `release-check.yaml` sets `contents: read` (`:18`).
  - The dry run stamps `9.9.9` (`release-dry-run.sh:18`) and tags its layouts `ghcr.io/open-feature/<name>:latest` and `:v<version>` (`release-dry-run-distributions.sh:23`, `release-dry-run.sh:122-123`).
  - "Never push images" is not in CLAUDE.md, so D adds a rule instead of removing one.
  - The reference Dockerfile builds on `$BUILDPLATFORM` and its final stage only copies (`builder/reference/Dockerfile:10`, `:48-53`). In the testbed Dockerfile, the `certs` stage (`:33-41`) and `RUN mkdir "flags"` (`:56`) are the only steps that run on the target platform. The `WORKDIR` change leaves a final stage that only copies, and `WORKDIR` creates the directory with the same owner and mode as `mkdir`.
- **A pull request cannot change what `main` builds, short of being merged.**
  - The Go caches are saved only on `main` pushes, and a PR's caches are scoped to its own ref, so `main` never restores them.
  - Each dry run creates its own `docker-container` builder, so no BuildKit cache crosses runs.
- **Q1 (a)'s split is right:** a job that holds the token but runs no build.
- **Q2 (a) works on this plan.** The repository is public, so GitHub artifact attestations need no paid plan. ghcr storage for public packages is free.
- **The description reaches ghcr.** On a multi-arch image ghcr reads the description from the index annotations, not from config labels. The plan sets `index:` annotations.

## Blocker

### 1. `packages: write` is in every workflow's default token today, including a `pull_request_target` one, and a branch can always ask for it (R1, "Permissions", K2)

- **Step:** "Permissions", R1, P's K2 test.
- **Evidence:**
  - `gh api repos/open-feature-forking/openfeature-relay/actions/permissions/workflow` returns `"default_workflow_permissions":"write"`. Every job without a `permissions` key gets a token that can write packages.
  - Five workflows have no top-level `permissions`:
    - `build.yaml`;
    - `docs-check.yaml`;
    - `markdown-checks.yaml`, which runs on a push to **any** branch and on PRs;
    - `release-please.yaml`, whose hash K2 pins;
    - **`lint-pr.yaml`, which runs on `pull_request_target` (`:4`)**: it is triggered by fork PRs, runs with the base repository's token, and hands that token to two third-party actions.
  - R1's "`pull_request_target` is not used anywhere" is false.
  - The plan only adds `contents: read` to `build.yaml`. As soon as `publish-images` creates the 7 packages with `GITHUB_TOKEN`, ghcr gives **this repository's** workflows write access to them. That access is per repository, not per branch or job.
  - **Even with every workflow fixed,** a branch pushed to this repository can request `permissions: packages: write` in its own copy of `build.yaml`, on `push` or on its same-repository `pull_request`, and push to the packages. `main` is not protected (`branches/main/protection` returns 404, no rulesets), so today that is the same set of people as "can push to `main`". Still, it is not the lead's "never on PRs". A session that edits `publish-images` on a PR branch and rehearses it there would publish for real.
- **Fix:**
  - **Required, a repository setting (the lead's call):** set the default token to read (`gh api -X PUT -R … repos/open-feature-forking/openfeature-relay/actions/permissions/workflow -f default_workflow_permissions=read`).
  - Add a top-level `permissions` block to every workflow. `lint-pr.yaml` needs `pull-requests: write` for its sticky comment. `release-please.yaml` either gets a block, with its K2 hash re-recorded deliberately, or relies on the setting.
  - K2 checks **every** file in `.github/workflows/`, not only `build.yaml`:
    - a top-level `permissions` block;
    - no `packages: write` outside `publish-images`;
    - a hashed allowlist for `release-please.yaml`'s `docker/login-action` (`:88`, gated to `open-feature/flagd`).
  - Say in R1 what is actually enforced: *anyone who can push a branch here can push images*. If the lead wants a real "only from `main`":
    - an environment `ghcr` with a `main`-only deployment branch policy, holding a credential (a GitHub App token);
    - after the first push, each package's Actions access for this repository lowered to Read, so `GITHUB_TOKEN` alone cannot push.

    That is Q5 for the lead.

## Major

### 2. The testbed that is pushed is not the testbed that was tested, and on most `main` pushes it is not built at all (Q1, "testbed builds multi-arch", section 3)

- **Evidence:**
  - `make testbed-contract-both` builds with `docker build -f distributions/flagd-testbed/Dockerfile -t flagd-testbed:local .` (`Makefile:492`): the default `docker` driver. A `docker-container` builder that exports the 2-platform OCI layout has a separate cache, so "the second build hits the cache" does not hold. The testbed also compiles with `go build -a` (`Dockerfile:30`).
  - **The two builds also differ in content.** The `certs` stage generates a new RSA key on every build (`:37`). So the contract and the suites run against a different image, a different digest and a different key from the one that is pushed. Q1 (a)'s "the digest the dry run checked is the digest that is pushed" is false for flagd-testbed.
  - **The `testbed` job is path-gated (`build.yaml:841`).** In run 42079716561 on `main` it took 0 min (skipped), so it produced no artifact. `needs: [release-dry-run, testbed]` succeeds on a skipped testbed, and `publish-images` must not fail on a missing layout.
- **Fix:**
  - Build the 2-platform layout first.
  - Load its amd64 image into Docker from the layout (`skopeo copy oci-archive:…  docker-daemon:…`, or `docker buildx build --load` with the same builder **and** the key generated outside the build).
  - Run the contract against that image: a `TESTBED_IMAGE` override in the Makefile that skips the build.
  - `publish-images` pushes flagd-testbed only when the layout exists, and the step summary says when it is skipped.
  - Test: the publish rehearsal includes a run without the testbed layout.

### 3. The load and time figures are off by about 3× (R3, R4, Q1 (a) "~11 min", Q3 "~20 pushes a day")

- **Evidence:**
  - **Runs on `main`:** `build` ran on `main` pushes 76 times on 2026-11-03, 65 on 2026-11-04 and 58 on 2026-11-05 up to 18:00. Each of those runs changed something outside docs. That is about 60 a day, not 20.
  - **Jobs per run:** run 42079716561 already starts **18 jobs** that are not skipped. The two new parts and `publish-images` make **21, above the org limit of 20 in a single run**, before any PR runs.
  - **Job times:** the jobs of that run started up to 8 minutes after the run did (17:58:05 → 18:05:52), so the queue is there today. The `distributions` part took **16 min**, `flagd-proxy-standard` 12 min.
  - **The new `distribution-images` part** compiles 5 distributions × 2 variants × 2 platforms = **20 builds with `go build -a`** (`builder/reference/Dockerfile:39`, so the Go build cache does nothing). Today's `distributions` part does fewer image builds than that in 16 min, so "~11 min" has nothing behind it.
  - **R4's artifacts:** 0.5–1 GB per push × ~60 pushes is 30–60 GB of uploads a day.
- **Fix:**
  - Redo R3, R4 and Q3 with these numbers before the questions go to the lead.
  - B measures against a stated budget: no part longer than today's 16 min, and no more than 20 jobs per `main` run. The plan names in advance which parts merge if B goes over.
  - Consider publishing only when the image inputs changed. The testbed gate's pattern is the model. The stamped commit changes the digest every time, so this has to be decided by paths, not digests. It also cuts the tags in finding 5.
  - Mention the alternative that really builds once: the image copies the linux binaries that `release-build.sh` already compiled. It is no longer upstream's Dockerfile path, so it is the lead's call; it is not the default.

### 4. `concurrency: publish-images` drops `sha-` tags, and the `ls-remote` check rarely lets `:main` move ("`:main` never moves backwards")

- **Evidence:**
  - **Pending runs are cancelled.** GitHub keeps at most one running and one pending job per concurrency group. When a third arrives, the pending one is cancelled, whatever `cancel-in-progress` says.
  - At ~60 pushes a day in bursts (finding 3), the commits in the middle of a burst never get their `:sha-<short>` tag, and their `main` runs show a cancelled `publish-images`.
  - **`:main` stalls.** "Move `:main` only if `ls-remote` still equals `github.sha`" needs the head to stay put for the whole ~17 min dry run. In a busy hour `:main` moves only after the last push. If that last commit is red (10 of 55 finished `main` runs on 2026-11-05 failed), `:main` stays behind green commits that came after it.
- **Fix:**
  - No concurrency group on the push of `sha-` tags: they are independent and idempotent.
  - Move `:main` by compare-and-set: read the `org.opencontainers.image.revision` that `:main` points at now, and move it only if that commit is an ancestor of `github.sha` (`git merge-base --is-ancestor`, with enough history fetched).
  - If a group is wanted, put it on a small `move-main` job only. There, cancelling a pending run is the right behaviour.
  - Test: the rehearsal pushes an older commit after a newer one, and `:main` stays on the newer one.

### 5. Retention: ~850 `sha-` tags a day, and Q3 (b)'s cleanup action deletes the images it means to keep (Q3)

- **Evidence:**
  - **Tags:** 13 tags per push × ~60 pushes is ~850 `sha-` tags a day.
  - **Package versions:** in ghcr each multi-arch tag also creates untagged package versions: 2 platform manifests, 2 buildx attestation manifests, and Q2's attestation referrer. That is ~5,000 package versions a day across the 7 packages.
  - **The cleanup trap:** `actions/delete-package-versions` with untagged deletion, the usual setup, deletes the platform and attestation manifests. Every multi-arch tag is then broken and its attestations are gone.
  - **The rehearsal does not cover it:** `registry:2` does not model ghcr's package versions.
- **Fix:**
  - Put the real numbers in Q3.
  - Recommend deciding retention now, not "an issue if it becomes a nuisance": at this rate it is a nuisance within a week.
  - If (b): delete by tag age. A version is deleted only if no kept index references it. Never use "delete untagged".
  - The first run of the cleanup is checked on ghcr itself: `crane manifest` of a kept tag resolves both platforms, and `gh attestation verify` still passes.
  - Finding 3's "publish only when inputs changed" shrinks all of these numbers.

### 6. Base images: one unpinned, and the pinned ones never update (supply chain)

- **Evidence:**
  - **The unpinned one:** `FROM alpine/openssl AS certs` (`distributions/flagd-testbed/Dockerfile:33`) has no tag and no digest. It is a community image, not a Docker Official one. It generates the key and certificate that ship in a public image. The plan moves the stage to `$BUILDPLATFORM` but leaves it unpinned. Today's K-tests pin the k6 image (`test_workflows.py:138`) but not this one.
  - **No updates:** there is no `.github/dependabot.yml` or Renovate configuration. The digest pins of `golang:1.27-alpine`, `distroless/static:nonroot` and `busybox:1.38` therefore never move, and the public `:main` images keep shipping a base that only gets older.
  - **No scan of what is pushed:** `docker-local`'s Trivy scan covers flagd's image only (`build.yaml:606-618`), none of the 13 published ones.
- **Fix:**
  - Pin `alpine/openssl` by digest, or generate the certificate in the pinned `golang:1.27-alpine` stage (`apk add openssl` at `$BUILDPLATFORM`). Extend the pin test to every `FROM` in the Dockerfiles that are published.
  - Add Dependabot for the `docker` ecosystem on those Dockerfiles. That is a new bot in the repository, so it is the lead's call; add it to Q2.
  - `publish-images` runs a Trivy scan on each layout and only reports, so a base with known CVEs is visible before the push.

## Minor

### 7. "The digest checked is the digest pushed" holds only for the structural checks (Q1)

`release-dry-run-distributions.sh` parses the layout for its name, platforms and attestations. The runtime checks (entrypoint, user, `version --verbose`) run on a **separate** `--load` build (`image_build off --load`, `:160`), and the arm64 image never runs. Fix: load the amd64 image from the layout and run the checks on it, and say in the plan that arm64 is checked only structurally.

### 8. flagd-full has an existing build it could reuse; the plan should say why it doesn't

The `images` part already builds flagd's multi-arch OCI from `flagd/build.Dockerfile`, which is flagd-full's code. A push by digest carries no name, so that layout could be pushed as `flagd-full` without a new build.

The catch is Q4. Its labels would change flagd's image configuration, which K1's `image-config.golden` records under `Labels`. The plan's choice is reasonable, but it is a choice:
- the published flagd-full is built differently (the reference Dockerfile, a module build path) from the image BR-012 and K1 check;
- it costs 4 more compiles (finding 3).

State it in section 0 or 7.

### 9. K2's push test has to cover every workflow and every form of push

Today's K2 reads `build.yaml` only and looks for `push: true` and `docker/login-action` (`test_workflows.py:85-87`). The new test must cover:
- all of `.github/workflows/` (finding 1);
- every form of push: `--push`, `type=registry`, `push=true`, `docker push`, `docker login`, `crane`, `skopeo`, `oras`, and `actions/attest-build-provenance` with `push-to-registry`.

Outside CI, the `Makefile` has `docker-push-flagd` with `buildx --push` (`:60-61`). The new CLAUDE.md rule covers sessions; name the target in that rule.

### 10. README.md still says there are no images, and ghcr shows it

`README.md:9`: "Until then there are no releases and no images; build from source." ghcr shows the linked repository's README on each package page. D changes `try-it.md` but not the README. Add it to D, and keep the "not an official OpenFeature or CNCF project" note above the pull instructions.

### 11. `publish-images.sh` should hard-code where it may push

The layouts carry `ghcr.io/open-feature/<name>` names in their index annotations (`io.containerd.image.name`, `org.opencontainers.image.ref.name`). Two copy setups can go wrong:
- one that takes the destination from the layout points at the official namespace;
- `skopeo copy oci-archive:` without an explicit ref fails, because each layout carries two refs.

The token cannot write to `open-feature`, but the attempt is the failure this step most wants to avoid. Fix: the script holds the 7 destination names and refuses any other. The rehearsal asserts it.

### 12. Revision 2 should start from today's `main`

b26e0665 predates the close of step 15 (63ed237e) and step 16. Since then `build.yaml` changed by +309/−25 lines and `test_workflows.py` by +127. `test_jobs` now ends with `load-smoke` and `load-test`. The job counts and part lists in revision 2 should come from afa094e1 or later.

### 13. Fix the length of `<short>`

`git rev-parse --short` grows as the repository does, so `sha-` tags would change length over time. Use `${GITHUB_SHA::7}` (or 12) in the version, the tags and the docs alike.

### 14. flagd-testbed has no provenance or SBOM in the plan

Section 3's OCI check covers "the six distributions × two variants". The testbed build has no `--provenance=mode=max --sbom=true` and no check, so the only image without attestations would be the one with a private key inside. Add both to its build and to the check. Also state in R5 that the testbed's CA key is public by design (`custom-ca.key` comes from the public test-harness submodule and signs the certificate, `Dockerfile:35-41`), so its certificate proves nothing.
