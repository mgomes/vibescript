package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func fmtCommand(args []string) error {
	fs := flag.NewFlagSet("fmt", flag.ContinueOnError)
	fs.SetOutput(new(flagErrorSink))
	write := fs.Bool("w", false, "write result to source files instead of stdout")
	check := fs.Bool("check", false, "fail if any source file needs formatting")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targets := fs.Args()
	if len(targets) == 0 {
		return errors.New("vibes fmt: path required")
	}

	files, err := collectVibeFiles(targets)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}

	changedCount := 0
	for _, path := range files {
		originalBytes, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		original := string(originalBytes)
		formatted := formatVibeSource(original)
		changed := formatted != original
		if changed {
			changedCount++
		}

		switch {
		case *write && changed:
			info, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("stat %s: %w", path, err)
			}
			if err := os.WriteFile(path, []byte(formatted), info.Mode().Perm()); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
		case !*write && !*check:
			fmt.Print(formatted)
		}
	}

	if *check && changedCount > 0 {
		return fmt.Errorf("vibes fmt: %d file(s) need formatting", changedCount)
	}

	return nil
}

func collectVibeFiles(targets []string) ([]string, error) {
	seen := make(map[string]struct{})
	files := make([]string, 0)
	addFile := func(path string) {
		if filepath.Ext(path) != ".vibe" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		if _, ok := seen[abs]; ok {
			return
		}
		seen[abs] = struct{}{}
		files = append(files, abs)
	}

	for _, target := range targets {
		info, err := os.Stat(target)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", target, err)
		}
		if !info.IsDir() {
			addFile(target)
			continue
		}
		err = filepath.WalkDir(target, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			addFile(path)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", target, err)
		}
	}

	sort.Strings(files)
	return files, nil
}

func formatVibeSource(source string) string {
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	lines := strings.Split(normalized, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}

	joined := strings.Join(lines, "\n")
	joined = strings.TrimRight(joined, "\n")
	return joined + "\n"
}
