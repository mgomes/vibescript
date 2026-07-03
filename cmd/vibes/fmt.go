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
		return fmt.Errorf("collect files: %w", err)
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
	var files []string
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
	var out strings.Builder
	out.Grow(len(source) + 1)

	lineStart := 0
	pendingBlankLines := 0
	wrote := false
	for i := 0; i < len(source); {
		if source[i] != '\n' && source[i] != '\r' {
			i++
			continue
		}
		lineEnd := trimLineEnd(source, lineStart, i)
		wrote = appendFormattedLine(&out, source[lineStart:lineEnd], &pendingBlankLines, wrote)
		if source[i] == '\r' && i+1 < len(source) && source[i+1] == '\n' {
			i += 2
		} else {
			i++
		}
		lineStart = i
	}
	if lineStart < len(source) {
		lineEnd := trimLineEnd(source, lineStart, len(source))
		wrote = appendFormattedLine(&out, source[lineStart:lineEnd], &pendingBlankLines, wrote)
	}
	if !wrote {
		return "\n"
	}
	return out.String()
}

func appendFormattedLine(out *strings.Builder, line string, pendingBlankLines *int, wrote bool) bool {
	if line == "" {
		*pendingBlankLines = *pendingBlankLines + 1
		return wrote
	}
	for range *pendingBlankLines {
		out.WriteByte('\n')
	}
	*pendingBlankLines = 0
	out.WriteString(line)
	out.WriteByte('\n')
	return true
}

func trimLineEnd(source string, start, end int) int {
	for end > start && (source[end-1] == ' ' || source[end-1] == '\t') {
		end--
	}
	return end
}
