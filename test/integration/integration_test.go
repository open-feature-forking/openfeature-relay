package integration_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	flagd "github.com/open-feature/go-sdk-contrib/providers/flagd/pkg"
	"github.com/open-feature/go-sdk-contrib/tests/flagd/v2/testframework"
	"github.com/testcontainers/testcontainers-go"
)

func getGitCommitNative() string {
	repo, err := git.PlainOpen(".")
	if err != nil {
		return "no-git-repo"
	}

	ref, err := repo.Head()
	if err != nil {
		return "no-head"
	}

	hash := ref.Hash().String()
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

func getPlatform() string {
	switch runtime.GOARCH {
	case "amd64":
		return "linux/amd64"
	case "arm64":
		return "linux/arm64"
	default:
		return "linux/amd64" // Always return a valid platform
	}
}

// The Gherkin tag expressions of the two suites; TestTagSelectionMatchesBranchPoint pins them.
const (
	rpcTags       = "@rpc && ~@unixsocket && ~@targetURI && ~@sync && ~@metadata && ~@grace && ~@events && ~@customCert && ~@reconnect && ~@caching && ~@forbidden && ~@fractional-v1 && ~@fractional-v2 && ~@deprecated"
	inProcessTags = "@in-process && ~@unixsocket && ~@metadata && ~@contextEnrichment && ~@customCert && ~@forbidden && ~@sync-port && ~@sync-payload && ~@fractional-v1 && ~@fractional-v2 && ~@deprecated"
)

// rpcOutageTags is rpcTags with the scenarios that stop and restart the testbed (@reconnect, @events and @grace) included.
// TESTBED_RPC_OUTAGES=true selects it for the gated testbed-rpc-outages job (UC-022 Q16); the default tags stay as they are.
const rpcOutageTags = "@rpc && ~@unixsocket && ~@targetURI && ~@sync && ~@metadata && ~@customCert && ~@caching && ~@forbidden && ~@fractional-v1 && ~@fractional-v2 && ~@deprecated"

// testbedImageEnv names a testbed image, as name:tag, to run the suites against. When it is unset, TestMain builds
// flagd from this checkout into the upstream testbed image, as it always has (UC-022 Q14).
const testbedImageEnv = "FLAGD_TESTBED_IMAGE"

// testbedImage is the image and the tag the suites run against.
func testbedImage() (image, tag string) {
	if named := os.Getenv(testbedImageEnv); named != "" {
		if i := strings.LastIndex(named, ":"); i > strings.LastIndex(named, "/") {
			return named[:i], named[i+1:]
		}
		return named, "latest"
	}
	return "local-testbed-image", getGitCommitNative()
}

func TestMain(m *testing.M) {
	flag.Parse()

	// Setup: Build and start flagd-testbed container
	ctx := context.Background()
	var err error

	err = setupFlagdTestbed(ctx)
	if err != nil {
		panic(fmt.Sprintf("Failed to setup flagd testbed: %v", err))
	}

	// Run tests
	code := m.Run()

	os.Exit(code)
}

func setupFlagdTestbed(ctx context.Context) error {
	if os.Getenv(testbedImageEnv) != "" {
		return nil
	}
	// Build the testbed image with local flagd
	buildContext, err := filepath.Abs("../../") // Adjust path to your project root
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Build flagd first
	flagdImage, err := buildFlagdImage(ctx, buildContext)
	if err != nil {
		return fmt.Errorf("failed to build flagd image: %w", err)
	}

	// Build testbed image using the local flagd
	err = buildTestbedImage(ctx, buildContext, flagdImage)
	if err != nil {
		return fmt.Errorf("failed to build testbed image: %w", err)
	}

	return nil
}

func buildFlagdImage(ctx context.Context, buildContext string) (string, error) {
	imageName := "flagd:test-local"

	// Execute the docker buildx command
	cmd := exec.Command("docker", "buildx", "build",
		"--platform="+getPlatform(),
		"-t", imageName,
		"-f", "flagd/build.Dockerfile",
		".")

	cmd.Dir = buildContext

	// Suppress docker build output by redirecting stderr to /dev/null
	cmd.Stderr = nil

	// Print the exact command being run
	fmt.Printf("Running command: %s\n", strings.Join(cmd.Args, " "))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return imageName, fmt.Errorf("docker buildx failed: %v\nOutput: %s", err, output)
	}

	return imageName, nil
}

func buildTestbedImage(ctx context.Context, buildContext, flagdImage string) error {
	// Suppress testcontainers output by redirecting stderr to Discard
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	defer func() {
		os.Stderr = oldStderr
		w.Close()
		r.Close()
	}()

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    filepath.Join(buildContext, "./test-harness"), // Path to testbed directory
			Dockerfile: "./flagd/Dockerfile",
			BuildArgs: map[string]*string{
				"FLAGD_BASE_IMAGE": &flagdImage,
			},
			Tag:  getGitCommitNative(),
			Repo: "local-testbed-image",
		},
	}

	// Build the image
	_, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          false, // We just want to build, not start
	})
	if err != nil {
		return fmt.Errorf("failed to build testbed image: %w", err)
	}

	return nil
}

func TestRPC(t *testing.T) {

	// Setup testbed runner for RPC provider
	image, tag := testbedImage()
	runner := testframework.NewTestbedRunner(testframework.TestbedConfig{
		ResolverType:  testframework.RPC,
		TestbedConfig: "default", // Use default testbed configuration
		TestbedDir:    "../../test-harness",
		Image:         image,
		Tag:           tag,
		// needed to speed up reconnection to make reconnection tests faster
		ExtraOptions: []flagd.ProviderOption{
			flagd.WithRetryBackoffMs(100),
			flagd.WithRetryBackoffMaxMs(1000),
		},
	})
	defer runner.Cleanup()

	// Define feature paths - using flagd-testbed gherkin files
	featurePaths := []string{
		"./",
	}

	// Run tests with RPC-specific tags - exclude connection/event issues we won't tackle
	tags := rpcTags
	if os.Getenv("TESTBED_RPC_OUTAGES") == "true" {
		tags = rpcOutageTags
	}

	if err := runner.RunGherkinTestsWithSubtests(t, featurePaths, tags); err != nil {
		t.Fatalf("Gherkin tests failed: %v", err)
	}
}

func TestInProcess(t *testing.T) {

	// Setup testbed runner for InProcess provider
	image, tag := testbedImage()
	runner := testframework.NewTestbedRunner(testframework.TestbedConfig{
		ResolverType:  testframework.InProcess,
		TestbedConfig: "default", // Use default testbed configuration
		TestbedDir:    "../../test-harness",
		Image:         image,
		Tag:           tag,
		// needed to speed up reconnection to make reconnection tests faster
		ExtraOptions: []flagd.ProviderOption{
			flagd.WithRetryBackoffMs(100),
			flagd.WithRetryBackoffMaxMs(1000),
		},
	})
	defer runner.Cleanup()

	// Define feature paths - using flagd-testbed gherkin files
	featurePaths := []string{
		"./",
	}

	// Run tests with InProcess-specific tags.
	// These tests use the published go-sdk-contrib in-process provider (see sibling go.mod),
	// not the local flagd evaluator code. The goal is to verify the sync.proto interface,
	// not to fully test the go in-process provider (that happens in go-sdk-contrib).
	// Many tags are excluded because they require a more complex testbed than what's built here.
	tags := inProcessTags

	if err := runner.RunGherkinTestsWithSubtests(t, featurePaths, tags); err != nil {
		t.Fatalf("Gherkin tests failed: %v", err)
	}
}
