package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/user/iwork-redline-detector/detector"
)

var (
	debugFlag   = flag.Bool("debug", false, "Show detailed information (insertions, deletions, etc.)")
	threadsFlag = flag.Int("threads", 2, "Number of concurrent threads")
)

func main() {
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Println("Usage: iwork-redline-detector [-debug] [-threads N] <path-to.pages-file-or-directory>")
		flag.PrintDefaults()
		os.Exit(1)
	}

	path := flag.Arg(0)

	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var pagesFiles []string

	if info.IsDir() {
		pagesFiles, err = findPagesFiles(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding .pages files: %v\n", err)
			os.Exit(1)
		}
		if len(pagesFiles) == 0 {
			fmt.Println("No .pages files found in directory")
			os.Exit(0)
		}
	} else {
		pagesFiles = []string{path}
	}

	threads := *threadsFlag
	if threads < 1 {
		threads = 1
	}

	fmt.Printf("Processing %d file(s) with %d thread(s)...\n\n", len(pagesFiles), threads)

	if *debugFlag {
		fmt.Println("FILEPATH                     | REDLINES | INSERTIONS | DELETIONS")
		fmt.Println("----------------------------|-----------|------------|----------")
	}

	type result struct {
		file      string
		relPath   string
		detection *detector.RedlineDetection
		err       error
	}

	results := make(chan result, len(pagesFiles))

	sem := make(chan struct{}, threads)
	var wg sync.WaitGroup

	for _, file := range pagesFiles {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			relPath, _ := filepath.Rel(path, f)
			if relPath == "." {
				relPath = filepath.Base(f)
			}

			r, err := detector.DetectRedlines(f)
			results <- result{file: f, relPath: relPath, detection: r, err: err}
		}(file)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	redlinesFound := 0
	for r := range results {
		if r.err != nil {
			fmt.Printf("Error: %v (%s)\n", r.err, r.relPath)
			continue
		}

		hasRedlines := r.detection.TrackChangesStatus == detector.TCStatusEnabledWithChanges

		if hasRedlines {
			redlinesFound++
		}

		if *debugFlag {
			fmt.Printf("%-28s| %-9t | %-10d | %d\n",
				r.relPath,
				hasRedlines,
				r.detection.InsertionCount,
				r.detection.DeletionCount)
		} else {
			fmt.Printf("%s\t%t\n", r.relPath, hasRedlines)
		}
	}

	fmt.Printf("\n%d file(s) processed, %d with redlines\n", len(pagesFiles), redlinesFound)

	if redlinesFound > 0 {
		os.Exit(1)
	}
}

func findPagesFiles(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".pages" {
			files = append(files, path)
		}
		return nil
	})

	return files, err
}
