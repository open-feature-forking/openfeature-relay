# Use Case: Package Distribution as Image

## Overview

**Use Case ID:** UC-002  
**Use Case Name:** Package Distribution as Image  
**Primary Actor:** Distribution Builder  
**Secondary Actors:** Image Build Environment, Container Registry  
**Goal:** Ship a custom distribution as a multi-platform image in a container registry, built the same way flagd's own image is built today, so that Operators can run it.  
**Trigger:** Distribution Builder decides to ship a custom distribution as an image  
**Status:** Implemented  

**Requirements:** [FR-009, NFR-009, C-003](../requirements.md)

## Preconditions

- The distribution source of the custom distribution has been generated from its manifest (UC-001).
- An Image Build Environment that can build images for several platforms is available to the Distribution Builder.

## Main Success Scenario

1. Distribution Builder takes the reference Dockerfile that ships with the Builder and places it with the distribution source.
2. Distribution Builder chooses the image name, the tag and the target platforms.
3. Distribution Builder asks the Image Build Environment to build the image from the reference Dockerfile with the distribution name, the distribution version, the source revision and the build date.
4. Image Build Environment resolves the component modules of the distribution source and compiles the distribution once for every target platform.
5. Image Build Environment places each platform's distribution binary in a minimal runtime image that starts the distribution and runs it as a non-root user.
6. Image Build Environment combines the platform images into one multi-platform image under the chosen name and tag.
7. Distribution Builder publishes the image to the Container Registry.
8. Container Registry stores the image and confirms it under its tag.
9. Operators can pull the image by its name and tag on every target platform and run the custom distribution.

## Alternative Flows

### A1: Additional Target Platforms

**Trigger:** Distribution Builder needs platforms beyond the default ones, such as the further Linux platforms flagd builds images for today (step 2)  
**Flow:**

1. Distribution Builder adds the platforms to the target platforms.
2. Use case continues at step 3.

### A2: Local Image Only

**Trigger:** Distribution Builder wants to run the image locally without publishing it (step 2)  
**Flow:**

1. Distribution Builder chooses the platform of the local machine as the only target platform.
2. Image Build Environment builds the image as in steps 4 and 5 and makes it available to the local machine under the chosen name and tag.
3. Use case ends.

### A3: FIPS Build

**Trigger:** Distribution Builder needs a FIPS 140-3 build of the distribution (step 3)  
**Flow:**

1. Distribution Builder requests the FIPS build and adds the `-fips` suffix to the chosen tag.
2. Image Build Environment compiles the distribution against the certified Go cryptographic module, as flagd's FIPS build does today, and verifies that every binary was built that way.
3. Use case continues at step 5.

### A4: Compilation Fails for a Platform

**Trigger:** The distribution cannot be compiled for one of the target platforms, for example because a component module does not support it (step 4)  
**Flow:**

1. Image Build Environment reports the platform and the reason the compilation failed.
2. Image Build Environment produces no image for any platform.
3. Use case ends.

### A5: Container Registry Rejects the Image

**Trigger:** Container Registry refuses the image, for example because the Distribution Builder lacks permission for the repository or the tag is protected (step 8)  
**Flow:**

1. Container Registry reports its refusal to the Distribution Builder.
2. Distribution Builder obtains the permission or chooses another name or tag.
3. Use case continues at step 7.

### A6: Container Registry Unavailable

**Trigger:** Container Registry does not answer (step 8)  
**Flow:**

1. Distribution Builder learns that the Container Registry is unreachable.
2. Use case ends.

## Postconditions

### Success Postconditions

- The Container Registry holds one multi-platform image under the chosen name and tag, with an image for every target platform.
- Every platform image contains exactly the components of the distribution's manifest, at the same versions as a binary built from the same distribution source.
- Every platform image reports the distribution name, distribution version, source revision and build date given to the image build.

### Failure Postconditions

- No image is published under the chosen name and tag that lacks one of the target platforms.
- An image already in the Container Registry under the chosen name and tag is not changed.
- The distribution source and its manifest are unchanged.

## Business Rules

### BR-001: Default Target Platforms

Without a choice of their own, the Distribution Builder gets an image for `linux/amd64` and `linux/arm64`.

### BR-002: Same Contents as the Binary

The image is compiled from the same distribution source as the binary of UC-001, so the image and the binary contain the same components at the same versions.

### BR-003: Minimal Runtime Image

The runtime image holds the distribution binary and nothing needed only to build it: no Go Toolchain, no distribution source, no shell. The distribution runs as a fixed non-root user and is the image's entry point.

### BR-004: All Platforms or None

A multi-platform image is published only when the distribution compiled for every target platform.

### BR-005: Pinned Base Images

The reference Dockerfile pins its build image and its runtime image, so that the same distribution source and build inputs build the same image.

### BR-006: Image Names

The Distribution Builder chooses the image name and tag of a custom distribution; the reference Dockerfile names none. Names under `ghcr.io/open-feature/` belong to the standard distributions: `ghcr.io/open-feature/flagd-<distribution>`, and `ghcr.io/open-feature/flagd` for flagd-full. A custom distribution is not published under them.

### BR-007: FIPS Suffix

A FIPS build of a distribution carries the `-fips` suffix and is published beside the regular image, never in its place.

## Delivered by Later Use Cases

This use case is `Implemented`: the reference Dockerfile builds a multi-platform image from a distribution source, as an OCI layout and not pushed (`test/image`). It covers the default and additional platforms, the local image (A2), the FIPS build (A3), the failed compilation that builds no image (A4), the same components as the binary, the minimal non-root runtime image and the pinned base images. These parts are accepted gaps of this use case, because they are behaviour of the Container Registry and of Docker, not of the Relay, or are delivered by another use case:

| Gap | Delivered by |
|-----|--------------|
| Steps 7–8, A5 and A6: publishing to the Container Registry, its confirmation, its refusal and its absence. Nothing is pushed during the proof of concept | none; behaviour of the Container Registry (step 9 is proven on the local platform) |
| BR-006: the image names of the standard distributions under `ghcr.io/open-feature/`, and their release | UC-012 |
