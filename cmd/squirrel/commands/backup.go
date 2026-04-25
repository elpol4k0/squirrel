package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/elpol4k0/squirrel/internal/chunker"
	"github.com/elpol4k0/squirrel/internal/progress"
	"github.com/elpol4k0/squirrel/internal/repo"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	backupRepo     string
	backupSrc      string
	backupDryRun   bool
	backupTags     []string
	backupParallel int
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Back up a file or directory into a squirrel repository",
	Example: `  squirrel backup --repo /mnt/backup/myrepo --path /etc
  squirrel backup --repo /mnt/backup/myrepo --path ./bigfile.tar --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if backupRepo == "" {
			return fmt.Errorf("--repo is required")
		}
		if backupSrc == "" {
			return fmt.Errorf("--path is required")
		}
		return runBackup(backupRepo, backupSrc, backupDryRun, backupTags, backupParallel)
	},
}

func init() {
	backupCmd.Flags().StringVar(&backupRepo, "repo", "", "Repository path (required)")
	backupCmd.Flags().StringVar(&backupSrc, "path", "", "File or directory to back up (required)")
	backupCmd.Flags().StringVar(&backupSrc, "file", "", "Alias for --path")
	backupCmd.Flags().BoolVar(&backupDryRun, "dry-run", false, "Show what would be uploaded without writing anything")
	backupCmd.Flags().StringArrayVar(&backupTags, "tag", nil, "Tags to attach to the snapshot (repeatable)")
	backupCmd.Flags().IntVar(&backupParallel, "parallel", 0, "Number of parallel file uploads (0 = number of CPUs)")
	backupCmd.Flags().MarkHidden("file")
}

type backupStats struct {
	files       int64
	dirs        int64
	newChunks   int64
	dedupChunks int64
	newBytes    int64
	dedupBytes  int64
	totalBytes  int64
	bar         *progress.Bar
	sem         chan struct{} // limits concurrent file goroutines; nil = sequential
}

func runBackup(repoPath, srcPath string, dryRun bool, tags []string, parallel int) error {
	ctx := context.Background()

	if _, err := os.Lstat(srcPath); err != nil {
		return fmt.Errorf("stat %s: %w", srcPath, err)
	}

	password, err := readTerminalPassword("Repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(repoPath, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}

	workers := parallel
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	bar := progress.NewBytes("backup")
	var stats backupStats
	stats.bar = bar
	stats.sem = make(chan struct{}, workers)

	if dryRun {
		if err := dryRunScan(r, srcPath, &stats); err != nil {
			return err
		}
		fi, _ := os.Lstat(srcPath)
		fmt.Printf("\nPath:    %s (%s)\n", srcPath, humanBytes(fi.Size()))
		fmt.Printf("Chunks:  %d new, %d already in repo\n", stats.newChunks, stats.dedupChunks)
		fmt.Printf("Upload:  %s new data, %s skipped\n", humanBytes(stats.newBytes), humanBytes(stats.dedupBytes))
		fmt.Println("\n[dry-run] No data was written.")
		return nil
	}

	treeID, err := backupEntry(ctx, r, srcPath, &stats)
	bar.Finish()
	if err != nil {
		return err
	}

	if err := r.Flush(ctx); err != nil {
		return fmt.Errorf("flush: %w", err)
	}

	snap, err := repo.NewSnapshot([]string{srcPath}, tags)
	if err != nil {
		return err
	}
	snap.Tree = treeID.String()
	if err := r.SaveSnapshot(ctx, snap); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}

	fmt.Printf("Snapshot %s saved\n", snap.ID)
	fmt.Printf("Files: %d  Dirs: %d  Chunks: %d new / %d dedup  Data: %s new / %s skipped\n",
		stats.files, stats.dirs, stats.newChunks, stats.dedupChunks,
		humanBytes(stats.newBytes), humanBytes(stats.dedupBytes))
	return nil
}

func backupEntry(ctx context.Context, r *repo.Repo, path string, stats *backupStats) (repo.BlobID, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return repo.BlobID{}, err
	}
	if fi.IsDir() {
		return backupDir(ctx, r, path, stats)
	}
	contentIDs, _, err := backupFile(ctx, r, path, stats)
	if err != nil {
		return repo.BlobID{}, err
	}
	tree := repo.Tree{Nodes: []repo.TreeNode{{
		Name:    filepath.Base(path),
		Type:    "file",
		Size:    fi.Size(),
		Mode:    uint32(fi.Mode()),
		Content: contentIDs,
	}}}
	return saveTree(ctx, r, tree)
}

func backupDir(ctx context.Context, r *repo.Repo, dirPath string, stats *backupStats) (repo.BlobID, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return repo.BlobID{}, fmt.Errorf("read dir %s: %w", dirPath, err)
	}
	atomic.AddInt64(&stats.dirs, 1)

	type nodeResult struct {
		node repo.TreeNode
		err  error
	}
	results := make([]nodeResult, len(entries))

	var wg sync.WaitGroup
	for i, entry := range entries {
		childPath := filepath.Join(dirPath, entry.Name())
		fi, err := entry.Info()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: stat %s: %v\n", childPath, err)
			continue
		}

		if entry.IsDir() {
			// Recurse synchronously to preserve tree structure.
			subtreeID, err := backupDir(ctx, r, childPath, stats)
			results[i] = nodeResult{err: err}
			if err == nil {
				results[i].node = repo.TreeNode{
					Name:    entry.Name(),
					Type:    "dir",
					Size:    fi.Size(),
					Mode:    uint32(fi.Mode()),
					Subtree: subtreeID.String(),
				}
			}
		} else if entry.Type().IsRegular() {
			wg.Add(1)
			stats.sem <- struct{}{}
			idx, ent, info := i, entry, fi
			go func() {
				defer wg.Done()
				defer func() { <-stats.sem }()
				contentIDs, size, err := backupFile(ctx, r, filepath.Join(dirPath, ent.Name()), stats)
				if err != nil {
					results[idx] = nodeResult{err: err}
					return
				}
				results[idx] = nodeResult{node: repo.TreeNode{
					Name:    ent.Name(),
					Type:    "file",
					Size:    size,
					Mode:    uint32(info.Mode()),
					Content: contentIDs,
				}}
			}()
		}
	}
	wg.Wait()

	var nodes []repo.TreeNode
	for _, res := range results {
		if res.err != nil {
			return repo.BlobID{}, res.err
		}
		if res.node.Name != "" {
			nodes = append(nodes, res.node)
		}
	}
	return saveTree(ctx, r, repo.Tree{Nodes: nodes})
}

func backupFile(ctx context.Context, r *repo.Repo, path string, stats *backupStats) ([]string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var contentIDs []string
	var size int64

	err = chunker.Split(f, func(ch chunker.Chunk) error {
		size += int64(ch.Length)
		atomic.AddInt64(&stats.totalBytes, int64(ch.Length))
		if stats.bar != nil {
			stats.bar.Add(int(ch.Length))
		}

		id, uploaded, err := r.SaveBlob(ctx, repo.BlobData, ch.Data)
		if err != nil {
			return err
		}
		if uploaded {
			atomic.AddInt64(&stats.newChunks, 1)
			atomic.AddInt64(&stats.newBytes, int64(ch.Length))
		} else {
			atomic.AddInt64(&stats.dedupChunks, 1)
			atomic.AddInt64(&stats.dedupBytes, int64(ch.Length))
		}
		contentIDs = append(contentIDs, id.String())
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("chunk %s: %w", path, err)
	}
	atomic.AddInt64(&stats.files, 1)
	return contentIDs, size, nil
}

func saveTree(ctx context.Context, r *repo.Repo, tree repo.Tree) (repo.BlobID, error) {
	data, err := json.Marshal(tree)
	if err != nil {
		return repo.BlobID{}, fmt.Errorf("marshal tree: %w", err)
	}
	id, _, err := r.SaveBlob(ctx, repo.BlobTree, data)
	return id, err
}

// dryRunScan walks src and reports dedup stats without writing anything.
func dryRunScan(r *repo.Repo, path string, stats *backupStats) error {
	return filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		return chunker.Split(f, func(ch chunker.Chunk) error {
			id := repo.ComputeIDPublic(ch.Data)
			if r.Index.Has(id) {
				atomic.AddInt64(&stats.dedupChunks, 1)
				atomic.AddInt64(&stats.dedupBytes, int64(ch.Length))
			} else {
				atomic.AddInt64(&stats.newChunks, 1)
				atomic.AddInt64(&stats.newBytes, int64(ch.Length))
			}
			return nil
		})
	})
}

func readTerminalPassword(prompt string) ([]byte, error) {
	fmt.Print(prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return nil, fmt.Errorf("read password: %w", err)
	}
	return pw, nil
}

func humanBytes(n int64) string {
	const (
		KiB = 1024
		MiB = 1024 * KiB
		GiB = 1024 * MiB
	)
	switch {
	case n >= GiB:
		return fmt.Sprintf("%.2f GiB", float64(n)/GiB)
	case n >= MiB:
		return fmt.Sprintf("%.2f MiB", float64(n)/MiB)
	case n >= KiB:
		return fmt.Sprintf("%.2f KiB", float64(n)/KiB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func marshalTree(t repo.Tree) ([]byte, error) {
	var buf bytes.Buffer
	enc := newJSONEncoder(&buf)
	if err := enc.Encode(t); err != nil {
		return nil, fmt.Errorf("marshal tree: %w", err)
	}
	return buf.Bytes(), nil
}
