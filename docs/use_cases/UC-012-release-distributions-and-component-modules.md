# Use Case: Release Distributions and Component Modules

## Overview

**Use Case ID:** UC-012  
**Use Case Name:** Release Distributions and Component Modules  
**Primary Actor:** Maintainer  
**Secondary Actors:** GitHub Actions, Container Registry  
**Goal:** Publish a version of every changed component module and, with each flagd release, the manifests, binaries and images of all standard distributions, so that manifests can pin component versions and Operators can use the standard distributions directly.  
**Trigger:** Maintainer decides to release the changes merged on main since the last release  
**Status:** Draft  

**Requirements:** [FR-030, FR-031, NFR-009, C-009, C-011](../requirements.md)

## Preconditions

- Every component module, the core module and flagd are registered with the release automation, each with its own version and changelog.
- The manifest of each standard distribution (flagd-minimal, flagd-cloud, flagd-kubernetes, flagd-proxy, flagd-full) is kept in the repository.
- All changes on main since the last release are conventional commits.
- GitHub Actions keeps a release pull request open that proposes the next version of every module changed since its last release.

## Main Success Scenario

1. Maintainer opens the pending release pull request.
2. GitHub Actions shows, for each module changed since its last release, the proposed next version and the changelog entries derived from the commits that touched that module.
3. Maintainer reviews the proposed versions and the component module versions each standard distribution manifest pins.
4. Maintainer merges the release pull request.
5. GitHub Actions tags each released module with its release tag and creates a GitHub release with that module's changelog.
6. GitHub Actions confirms that each new tag resolves as a version of its component module.
7. GitHub Actions builds every standard distribution from its manifest against the released component module versions, as a standard build and a FIPS build, for every OS and architecture of the platform matrix.
8. GitHub Actions confirms that every FIPS build carries the certified Go Cryptographic Module and refuses to run with FIPS mode disabled.
9. GitHub Actions attaches each standard distribution's manifest, binary archives and software bill of materials to the flagd release.
10. GitHub Actions builds the standard and FIPS images of every standard distribution for `linux/amd64` and `linux/arm64`, pushes them to the Container Registry and signs them.
11. Maintainer sees the flagd release with the manifests, binaries and images of all five standard distributions, and the new component module versions available for manifests to pin.

## Alternative Flows

### A1: Proposed Version Is Wrong

**Trigger:** Maintainer disagrees with a proposed version, e.g. a breaking change was committed without a breaking-change marker (step 3)  
**Flow:**

1. Maintainer adds a commit to main that states the intended version of that module.
2. GitHub Actions updates the release pull request with the corrected version and changelog.
3. Use case continues at step 3.

### A2: Manifest Pins an Unreleased Component Version

**Trigger:** A standard distribution manifest pins a component module version that is neither released nor part of this release (step 3)  
**Flow:**

1. Maintainer does not merge the release pull request.
2. Maintainer corrects the manifest on main to pin a released version or a version this release creates.
3. GitHub Actions updates the release pull request.
4. Use case continues at step 3.

### A3: Only Component Modules Are Released

**Trigger:** The release contains new versions of component modules or the core module but no new flagd version (step 5)  
**Flow:**

1. GitHub Actions tags and releases only those modules.
2. GitHub Actions builds and publishes no standard distribution.
3. Maintainer sees the new component module versions available for manifests to pin.
4. Use case ends.

### A4: Tag Does Not Resolve as a Module Version

**Trigger:** A new tag does not resolve as a version of its component module, e.g. the module path does not match its directory (step 6)  
**Flow:**

1. GitHub Actions stops the release before any standard distribution is built and names the module and tag.
2. Maintainer fixes the module on main; the fix is released as the next version of that module.
3. Use case ends.

### A5: Distribution Build Fails

**Trigger:** A standard distribution does not build for one OS and architecture, or one of its FIPS builds fails its check (step 7 or step 8)  
**Flow:**

1. GitHub Actions publishes no binary and no image of that standard distribution and names the distribution, platform and variant that failed.
2. Maintainer re-runs the release jobs when the cause is transient; otherwise the fix is released as the next flagd version.
3. Use case continues at step 7 after a re-run, or ends.

### A6: Container Registry Rejects the Images

**Trigger:** The Container Registry is unavailable or rejects a push (step 10)  
**Flow:**

1. GitHub Actions reports the image that was not pushed.
2. Maintainer re-runs the image jobs once the Container Registry accepts pushes again.
3. Use case continues at step 10.

## Postconditions

### Success Postconditions

- Every module released has a release tag and a GitHub release with its changelog, and manifests can pin that version.
- With a new flagd version, all five standard distributions are published under that version: manifest, standard and FIPS binary archives for the platform matrix, software bill of materials, and signed standard and FIPS images for `linux/amd64` and `linux/arm64`.
- flagd-full is published as `ghcr.io/open-feature/flagd` and flagd-proxy as `ghcr.io/open-feature/flagd-proxy`, each with the same image tags and archive names as before the builder.
- The release state records the new version of every released module.

### Failure Postconditions

- No existing release tag is moved or deleted.
- No standard distribution is published with only part of its platform matrix, or without its FIPS build.
- No `latest` image tag points at an image of a standard distribution whose release did not complete.
- No published manifest pins a component module version that has no release tag.

## Business Rules

### BR-001: Release Tag Format

The release tag of a module is its directory path in the repository, a slash, `v` and its semantic version (e.g. `core/v0.20.0`, `sync/file/v0.1.0`). This is the tag form from which a module version in a subdirectory resolves, and the form today's release automation already produces for `core`, `flagd` and `flagd-proxy`.

### BR-002: Version From Conventional Commits

The next version of a module is derived only from the conventional commits that touched that module's directory since its last release. Before version 1.0.0, a breaking change bumps the minor version and a feature bumps the patch version, as for today's modules.

### BR-003: Standard Distributions Follow the flagd Version

All five standard distributions are released together under the flagd version. A standard distribution has no version of its own.

### BR-004: Published Manifests Pin Released Versions

A published standard distribution manifest references every component module by an exact released version and contains no replacements.

### BR-005: Components Before Distributions

Within one release, every component module tag is created and resolves before any standard distribution is built, so a manifest may pin a version created by the same release.

### BR-006: Platform Matrix

Standard binaries are built for darwin/arm64, darwin/amd64, linux/arm64, linux/amd64, windows/amd64 and windows/386. FIPS binaries are built for the same platforms except windows/386. Images of both variants are built for `linux/amd64` and `linux/arm64`.

### BR-007: FIPS Build of Every Standard Distribution

Every standard distribution has a FIPS build with the same crypto boundary as today's FIPS build: built with the certified Go Cryptographic Module v1.0.0 and published with the `-fips` suffix on its archives and image tags.

### BR-008: Complete Distribution or None

The binaries and images of a standard distribution are published only when all its builds of BR-006 and BR-007 succeed.

### BR-009: Signed and Described Artifacts

Every image is signed and carries provenance and a software bill of materials. Every binary archive contains the license, the changelog, the README and the software bill of materials.

### BR-010: Released Versions Are Immutable

A released version is never moved, deleted or republished with different content. A faulty release is fixed by the next version.

### BR-011: Signed-Off Release Commits

The release pull request and its commits carry a DCO sign-off.

### BR-012: flagd-proxy Continues Today's Release

The flagd-proxy standard distribution, a configuration proxy, takes the place of today's separately released flagd-proxy: it keeps its image name `ghcr.io/open-feature/flagd-proxy` and its archive names, and from its first release as a standard distribution it follows the flagd version (BR-003).
