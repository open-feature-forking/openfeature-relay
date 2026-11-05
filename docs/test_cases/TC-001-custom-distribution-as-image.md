# Test Case: Custom Distribution as Image

## Overview

**ID:** TC-001  
**Goal:** A Distribution Builder turns a manifest into a stamped custom distribution, confirms its components with `flagd components`, and packages it as a multi-platform image that runs as a non-root user.  
**Priority:** Critical  
**Status:** Draft

## Roles

- Distribution Builder (writes the manifest, runs the Builder and the image build)
- Operator (inspects the distribution's components)

## Preconditions

- The manifest `test/distributions/example/manifest.yaml` exists; it selects the example service endpoint `github.com/open-feature/flagd/test/service-example` next to official components.
- The reference Dockerfile `builder/reference/Dockerfile` exists.
- Docker with Buildx and QEMU is available for the image steps (`upgrade-test` job).

## Flow

| Step | Name | Description | Test Data | Use Case | Covered by |
|------|------|-------------|-----------|----------|------------|
| 1 | Write manifest | The Distribution Builder writes a manifest with the distribution's name, version, output location and its components grouped by component type | `test/distributions/example/manifest.yaml` | [UC-001](../use_cases/UC-001-build-custom-distribution.md) | `TestValidManifest`, `TestCmdEntry` (`builder/internal/manifest`) |
| 2 | Run Builder | The Distribution Builder runs the Builder on the manifest, which validates it and reports every faulty entry with its position | `relay-builder --manifest test/distributions/example/manifest.yaml` | [UC-001](../use_cases/UC-001-build-custom-distribution.md) | `TestCLIValidateOnly`, `TestReleaseFlagsReachTheBuild` (`builder/cmd/relay-builder`); `TestManifestValidation`, `TestEveryProblemIsReportedWithItsPosition` (`builder/internal/manifest`) |
| 3 | Generate source | The Builder generates the entry point and the module definition, the same on every run | output location `./build` | [UC-001](../use_cases/UC-001-build-custom-distribution.md) | `TestGoldenFiles`, `TestReproducibleSource`, `TestPinnedRuntimeAndEveryModuleRequiredOnce` (`builder/internal/generate`); `check-flagd-full-generated` (`builder-e2e` and `integration-test` jobs) |
| 4 | Resolve modules | The Go Toolchain resolves the component modules, and the Builder checks each resolved at its declared version | - | [UC-001](../use_cases/UC-001-build-custom-distribution.md) | `TestUnresolvableModule`, `TestResolvedVersionDiffers`, `TestReplaceSkipsVersionCheck` (`builder/internal/gotool`); `TestManifestMissingAReplaceIsReported` (`test/distributions`) |
| 5 | Check needs and schemes | The Builder checks that every need of a selected component is met and that every scheme has one sync provider | - | [UC-001](../use_cases/UC-001-build-custom-distribution.md) | `TestUnmetNeeds`, `TestSchemeClaimedTwice` (`builder/internal/needs`); `TestUnmetNeedStopsBeforeCompiling` (`builder/internal/build`) |
| 6 | Compile distribution | The Builder compiles one binary stamped with name, version, source revision and build date, and reports where the binary and the source are | `--dist-version v9.9.9 --commit abc1234 --date 2026-01-02T03:04:05Z` | [UC-001](../use_cases/UC-001-build-custom-distribution.md) | `TestFullBuildStampsTheBinary`, `TestSkipCompileLeavesTheSource` (`builder/internal/build`); `TestBuildExampleDistribution`, `TestOnlySelectedComponents` (`test/distributions`) |
| 7 | Verify distribution serves | The built distribution starts and answers through the selected endpoint | flag `greeting`, value `hello` | - | `TestBuildExampleDistribution` (`test/distributions`) |
| 8 | Inspect components | The Operator runs `flagd components` and sees the distribution's name and version, its components grouped by type, each sync provider's schemes and each component's module and version | `example components --output json` | [UC-011](../use_cases/UC-011-inspect-distribution-components.md) | `TestComponentsMatchesManifest` (`test/distributions`); `TestComponentsListsInventory`, `TestComponentsIdentityMatchesVersionVerbose`, `TestComponentsJSONSameContentAsText` (`runtime/cmd`); `TestInventoryVersionsFromBuildInfo` (`core/pkg/distribution`) |
| 9 | Verify components match manifest | The listed components are exactly the ones the manifest selects | - | - | `TestComponentsMatchesManifest` (`test/distributions`) |
| 10 | Build image | The Distribution Builder builds the image from the reference Dockerfile, compiled once per target platform and combined into one multi-platform image | `relay-image-test/example:standard`, `linux/amd64,linux/arm64`, `VERSION=v9.9.9` | [UC-002](../use_cases/UC-002-package-distribution-as-image.md) | `TestReferenceDockerfilePins` (`test/image`, also in the `test` job); `TestReferenceImageMultiPlatform` (`test/image`) |
| 11 | Publish image | The Distribution Builder publishes the image to the Container Registry, which confirms it under its tag | `relay-image-test/example:standard` | [UC-002](../use_cases/UC-002-package-distribution-as-image.md) | gap |
| 12 | Run image | The image starts the distribution as user `65532:65532` with no shell, with the same components as the binary | - | [UC-002](../use_cases/UC-002-package-distribution-as-image.md) | `TestReferenceImageRuns`, `TestImageSameComponentsAsBinary` (`test/image`) |

The `builder/...` and `test/distributions` tests run in the `test` job on every pull request. The `test/image` tests run in the `upgrade-test` job (`make image-test`) when a change touches a Dockerfile, `distributions/`, `builder/` or the build, and on every `FULL_CI` or manual run.

## Validation

1. **Exact contents**: The binary and the image contain exactly the components the manifest lists, as `flagd components` shows them.
2. **Stamped identity**: The binary and the image report the distribution's name and version given at build time.
3. **Multi-platform image**: The image index lists `linux/amd64` and `linux/arm64`, and the image runs as a non-root user.

## Postconditions

- The generated source and the `example` binary in the manifest's output location.
- The local image `relay-image-test/example:standard`; removing it leaves the binary untouched.
- The manifest is unchanged.

## Gaps

| Step | Gap |
|------|-----|
| 11 | Nothing is pushed to a Container Registry during the proof of concept (UC-002 gap table); step 12 runs the image on the local platform, not pulled from a registry. |
