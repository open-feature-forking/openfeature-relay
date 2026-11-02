# Use Case: Package Distribution as Image

## Overview

**Use Case ID:** UC-002  
**Use Case Name:** Package Distribution as Image  
**Primary Actor:** Distribution Builder  
**Secondary Actors:** Container Registry  
**Goal:** Ship a custom distribution as a multi-platform image in a container registry, built the same way flagd's own image is built today, so that Operators can run it.  
**Trigger:** Distribution Builder decides to ship a custom distribution as an image  
**Status:** Draft  

**Requirements:** [FR-009, NFR-009](../requirements.md)

## Preconditions

- The distribution source of the custom distribution has been generated from its manifest (UC-001).
- An image build environment that can build images for several platforms is available to the Distribution Builder.

## Main Success Scenario

1. Distribution Builder takes the reference Dockerfile that ships with the Builder and places it with the distribution source.
2. Distribution Builder chooses the image name, the tag and the target platforms.
3. Distribution Builder starts the image build with the distribution version, the source revision and the build date.
4. System resolves the component modules of the distribution source and compiles the distribution once for every target platform.
5. System places each platform's distribution binary in a minimal runtime image that starts the distribution and runs it as a non-root user.
6. System combines the platform images into one multi-platform image under the chosen name and tag.
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
2. System builds the image as in steps 4 and 5 and makes it available to the local machine under the chosen name and tag.
3. Use case ends.

### A3: Compilation Fails for a Platform

**Trigger:** The distribution cannot be compiled for one of the target platforms, for example because a component module does not support it (step 4)  
**Flow:**

1. System reports the platform and the reason the compilation failed.
2. System produces no image for any platform.
3. Use case ends.

### A4: Container Registry Rejects the Image

**Trigger:** Container Registry refuses the image, for example because the Distribution Builder lacks permission for the repository or the tag is protected (step 8)  
**Flow:**

1. System reports the Container Registry's refusal.
2. Distribution Builder obtains the permission or chooses another name or tag.
3. Use case continues at step 7.

### A5: Container Registry Unavailable

**Trigger:** Container Registry does not answer (step 8)  
**Flow:**

1. System reports that the Container Registry is unreachable.
2. Use case ends.

## Postconditions

### Success Postconditions

- The Container Registry holds one multi-platform image under the chosen name and tag, with an image for every target platform.
- Every platform image contains exactly the components of the distribution's manifest, at the same versions as a binary built from the same distribution source.
- Every platform image reports the distribution version, source revision and build date given to the image build.

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

### BR-006: Image Name Chosen by the Distribution Builder

The reference Dockerfile names no image; the Distribution Builder chooses name and tag. A custom distribution is not published under `ghcr.io/open-feature/flagd`, the image name of flagd-full.
