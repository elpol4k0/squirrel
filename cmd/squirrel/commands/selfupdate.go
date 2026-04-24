package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const githubRepo = "elpol4k0/squirrel"

// Version is set via -ldflags at build time.
var Version = "dev"

var selfUpdateCmd = &cobra.Command{
	Use:   "self-update",
	Short: "Update squirrel to the latest release from GitHub",
	RunE: func(cmd *cobra.Command, args []string) error {
		check, _ := cmd.Flags().GetBool("check")
		return runSelfUpdate(check)
	},
}

func init() {
	selfUpdateCmd.Flags().Bool("check", false, "only check for updates, do not install")
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Body    string    `json:"body"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func runSelfUpdate(checkOnly bool) error {
	release, err := latestRelease()
	if err != nil {
		return err
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(Version, "v")

	fmt.Printf("current: %s\nlatest:  %s\n", current, latest)

	if latest == current || latest == "dev" {
		fmt.Println("already up to date")
		return nil
	}

	if checkOnly {
		fmt.Printf("update available: %s → %s\n", current, latest)
		return nil
	}

	assetName := fmt.Sprintf("squirrel_%s_%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		assetName += ".exe"
	}

	var downloadURL string
	for _, a := range release.Assets {
		if strings.Contains(a.Name, runtime.GOOS) && strings.Contains(a.Name, runtime.GOARCH) {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("no asset found for %s/%s in release %s", runtime.GOOS, runtime.GOARCH, release.TagName)
	}

	fmt.Printf("downloading %s...\n", assetName)
	newBin, err := downloadBinary(downloadURL)
	if err != nil {
		return err
	}

	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find own executable: %w", err)
	}

	tmp := selfPath + ".new"
	if err := os.WriteFile(tmp, newBin, 0o755); err != nil {
		return fmt.Errorf("write new binary: %w", err)
	}

	// Rename: on Windows we need to move the old binary aside first
	old := selfPath + ".old"
	os.Rename(selfPath, old) //nolint:errcheck
	if err := os.Rename(tmp, selfPath); err != nil {
		os.Rename(old, selfPath) //nolint:errcheck
		return fmt.Errorf("replace binary: %w", err)
	}
	os.Remove(old) //nolint:errcheck

	fmt.Printf("updated to %s\n", release.TagName)
	return nil
}

func latestRelease() (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", githubRepo)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github api returned %d", resp.StatusCode)
	}
	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("parse release: %w", err)
	}
	return &release, nil
}

func downloadBinary(url string) ([]byte, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
