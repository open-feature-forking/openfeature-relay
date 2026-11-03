package azblob

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gocloud.dev/blob"
)

const (
	helperURI = "AZBLOB_TEST_URI"
	// connectionString is a well-formed connection string for a made-up account
	connectionString = "DefaultEndpointsProtocol=https;AccountName=acct;AccountKey=a2V5;EndpointSuffix=core.windows.net"
)

// TestOpenBucketHelper opens the bucket named by helperURI and reports the outcome on stdout. gocloud reads
// the Azure environment variables once, when the first bucket is opened, so each environment needs a process
// of its own: TestEnvironmentCharacterization runs the test binary with this test selected.
func TestOpenBucketHelper(t *testing.T) {
	uri := os.Getenv(helperURI)
	if uri == "" {
		t.Skip("runs only as a helper process")
	}
	b, err := blob.OpenBucket(context.Background(), uri)
	if b != nil {
		_ = b.Close()
	}
	if err != nil {
		fmt.Printf("RESULT: error: %v\n", err)
		return
	}
	fmt.Println("RESULT: opened")
}

// TestEnvironmentCharacterization pins which environment variables and URL options gocloud's Azure driver
// accepts, and how it fails for the ones it rejects.
func TestEnvironmentCharacterization(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		uri  string
		want string
	}{
		{"no account", map[string]string{}, "azblob://bucket", "error: azureblob: Options.AccountName is required"},
		{"account only: default credential", map[string]string{"AZURE_STORAGE_ACCOUNT": "acct"}, "azblob://bucket", "opened"},
		{"account in the URL", map[string]string{}, "azblob://bucket?storage_account=acct", "opened"},
		{
			"shared key", map[string]string{"AZURE_STORAGE_ACCOUNT": "acct", "AZURE_STORAGE_KEY": "a2V5"}, "azblob://bucket",
			"opened",
		},
		{
			"a shared key that is not base64 fails", map[string]string{"AZURE_STORAGE_ACCOUNT": "acct", "AZURE_STORAGE_KEY": "!!"},
			"azblob://bucket", "error: failed azblob.NewSharedKeyCredential: decode account key: illegal base64 data at input byte 0",
		},
		{
			"a key without an account", map[string]string{"AZURE_STORAGE_KEY": "a2V5"}, "azblob://bucket",
			"error: azureblob: Options.AccountName is required",
		},
		{
			"sas token", map[string]string{"AZURE_STORAGE_ACCOUNT": "acct", "AZURE_STORAGE_SAS_TOKEN": "sv=2022&sig=abc"},
			"azblob://bucket", "opened",
		},
		{
			"connection string", map[string]string{"AZURE_STORAGE_ACCOUNT": "acct", "AZURE_STORAGE_CONNECTION_STRING": connectionString},
			"azblob://bucket", "opened",
		},
		{
			"the other connection string variable",
			map[string]string{"AZURE_STORAGE_ACCOUNT": "acct", "AZURE_STORAGEBLOB_CONNECTIONSTRING": connectionString},
			"azblob://bucket", "opened",
		},
		{
			"a connection string that cannot be parsed fails",
			map[string]string{"AZURE_STORAGE_ACCOUNT": "acct", "AZURE_STORAGE_CONNECTION_STRING": "garbage"},
			"azblob://bucket", "error: connection string is either blank or malformed.",
		},
		{
			"domain, protocol, cdn and emulator variables",
			map[string]string{
				"AZURE_STORAGE_ACCOUNT": "acct", "AZURE_STORAGE_DOMAIN": "blob.example.com", "AZURE_STORAGE_PROTOCOL": "http",
				"AZURE_STORAGE_IS_CDN": "true", "AZURE_STORAGE_IS_LOCAL_EMULATOR": "true",
			},
			"azblob://bucket", "opened",
		},
		{
			"url options", map[string]string{"AZURE_STORAGE_ACCOUNT": "acct"},
			"azblob://bucket?domain=blob.example.com&protocol=http&cdn=true&localemu=true", "opened",
		},
		{
			"an invalid cdn value", map[string]string{"AZURE_STORAGE_ACCOUNT": "acct"}, "azblob://bucket?cdn=maybe",
			`error: strconv.ParseBool: parsing "maybe": invalid syntax`,
		},
		{
			"an unknown url option", map[string]string{"AZURE_STORAGE_ACCOUNT": "acct"}, "azblob://bucket?foo=bar",
			`error: unknown query parameter "foo"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(os.Args[0], "-test.run=^TestOpenBucketHelper$", "-test.v")
			cmd.Env = []string{helperURI + "=" + tt.uri}
			for k, v := range tt.env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))

			var got string
			for _, line := range strings.Split(string(out), "\n") {
				if rest, ok := strings.CutPrefix(line, "RESULT: "); ok {
					got = rest
				}
			}
			require.True(t, strings.HasPrefix(got, tt.want), "got %q, want a result starting with %q", got, tt.want)
		})
	}
}
