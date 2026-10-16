package logstore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type GroupInfo struct {
	Name  string
	Count int
	Bytes int64
}

type StorageInfo struct {
	Root   string
	Count  int
	Bytes  int64
	Groups []GroupInfo
}

// Inspect counts disk bytes, not decompressed bytes. It reads directory
// metadata only, so even a large compressed archive requires no decompression.
func Inspect(root string) (StorageInfo, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return StorageInfo{}, err
	}
	result := StorageInfo{Root: abs}
	entries, err := ListLogFiles(root)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	groups := map[string]*GroupInfo{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return result, err
		}
		group := filepath.Dir(entry.RelativePath)
		if group == "." {
			group = "unclassified"
		}
		if groups[group] == nil {
			groups[group] = &GroupInfo{Name: group}
		}
		g := groups[group]
		g.Count++
		g.Bytes += info.Size()
		result.Count++
		result.Bytes += info.Size()
	}
	for _, group := range groups {
		result.Groups = append(result.Groups, *group)
	}
	sort.Slice(result.Groups, func(i, j int) bool { return result.Groups[i].Name < result.Groups[j].Name })
	return result, nil
}

func WriteInfo(w io.Writer, root string) error {
	info, err := Inspect(root)
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Storage: %s\nLogs: %d\nDisk usage: %s (%d bytes)\n", info.Root, info.Count, byteSize(info.Bytes), info.Bytes)
	if len(info.Groups) > 0 {
		fmt.Fprintln(&b, "\nGroup\tLogs\tDisk usage")
		for _, g := range info.Groups {
			fmt.Fprintf(&b, "%s\t%d\t%s (%d bytes)\n", g.Name, g.Count, byteSize(g.Bytes), g.Bytes)
		}
	}
	_, err = io.WriteString(w, b.String())
	return err
}

func byteSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	v := float64(size)
	unit := "B"
	for _, u := range []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"} {
		v /= 1024
		unit = u
		if v < 1024 {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", v, unit)
}
