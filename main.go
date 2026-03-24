package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/rodaine/table"
	"github.com/schollz/progressbar/v3"
	"github.com/xifanyan/iwork-redline-detector/detector"
)

var (
	debugFlag    = flag.Bool("debug", false, "Show detailed information (insertions, deletions, etc.)")
	csvFlag      = flag.String("csv", "", "Output results as CSV to specified file")
	threadsFlag  = flag.Int("threads", 2, "Number of concurrent threads")
	filelistFlag = flag.String("filelist", "", "Path to txt file containing list of .pages files (one per line)")
)

func main() {
	flag.Parse()

	if *filelistFlag != "" && flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "Error: cannot use both positional path and --filelist flag")
		fmt.Println("Usage: iwork-redline-detector [-debug] [-csv <filename>] [-threads N] [-filelist <path-to-file>] <path-to.pages-file-or-directory>")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if flag.NArg() != 1 && *filelistFlag == "" {
		fmt.Println("Usage: iwork-redline-detector [-debug] [-csv <filename>] [-threads N] [-filelist <path-to-file>] <path-to.pages-file-or-directory>")
		flag.PrintDefaults()
		os.Exit(1)
	}

	var pagesFiles []string
	var basePath string
	var err error

	if *filelistFlag != "" {
		pagesFiles, err = readFilelist(*filelistFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading filelist: %v\n", err)
			os.Exit(1)
		}
		basePath = ""
	} else {
		path := flag.Arg(0)
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if info.IsDir() {
			pagesFiles, err = findPagesFiles(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error finding .pages files: %v\n", err)
				os.Exit(1)
			}
		} else {
			if !strings.HasSuffix(path, ".pages") {
				fmt.Fprintf(os.Stderr, "Error: %s is not a .pages file\n", path)
				os.Exit(1)
			}
			pagesFiles = []string{path}
		}
		basePath = path
	}

	threads := *threadsFlag
	if threads < 1 {
		threads = 1
	}

	bar := progressbar.NewOptions(len(pagesFiles),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetDescription("[cyan]Processing..."),
		progressbar.OptionOnCompletion(func() {
			fmt.Fprint(os.Stderr, "\n")
		}),
		progressbar.OptionSetTheme(progressbar.ThemeASCII),
	)
	fmt.Printf("Processing %d file(s) with %d thread(s)...\n\n", len(pagesFiles), threads)

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

			var relPath string
			if basePath == "" {
				relPath = f
			} else {
				relPath, _ = filepath.Rel(basePath, f)
				if relPath == "." {
					relPath = filepath.Base(f)
				}
			}

			r, err := detector.DetectRedlines(f)
			results <- result{file: f, relPath: relPath, detection: r, err: err}
		}(file)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	type row struct {
		filePath       string
		hasRedlines    bool
		insertionCount int
		deletionCount  int
		comments       string
		redlineSource  string
		status         string
		confidence     string
		format         string
	}

	var rows []row
	for res := range results {
		bar.Add(1)
		if res.err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %v\n", res.file, res.err)
			continue
		}

		d := res.detection
		hasRedlines := d.HasRedlines()

		comments := ""
		if d.HasComments {
			comments = fmt.Sprintf("%d", d.CommentCount)
		}

		redlineSource := ""
		switch {
		case d.HasComments && d.HasTrackedChanges():
			redlineSource = "Tracked Changes + Comments"
		case d.HasComments:
			redlineSource = "Comments"
		case d.HasTrackedChanges():
			redlineSource = "Tracked Changes"
		}

		rows = append(rows, row{
			filePath:       res.relPath,
			hasRedlines:    hasRedlines,
			insertionCount: d.InsertionCount,
			deletionCount:  d.DeletionCount,
			comments:       comments,
			redlineSource:  redlineSource,
			status:         d.TrackChangesStatus.String(),
			confidence:     map[bool]string{true: "High", false: "Low"}[d.HighConfidence],
			format:         d.Format.String(),
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		return rows[i].filePath < rows[j].filePath
	})

	if *debugFlag {
		tbl := table.New("FILEPATH", "REDLINES", "INSERTIONS", "DELETIONS", "COMMENTS", "SOURCE", "STATUS", "CONF", "FORMAT")
		for _, r := range rows {
			tbl.AddRow(r.filePath, r.hasRedlines, r.insertionCount, r.deletionCount, r.comments, r.redlineSource, r.status, r.confidence, r.format)
		}
		tbl.Print()
	} else {
		tbl := table.New("FILEPATH", "REDLINES", "FORMAT")
		for _, r := range rows {
			tbl.AddRow(r.filePath, r.hasRedlines, r.format)
		}
		tbl.Print()
	}

	if *csvFlag != "" {
		file, err := os.Create(*csvFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating CSV file: %v\n", err)
			os.Exit(1)
		}
		defer file.Close()
		fmt.Fprintln(file, "filepath,redlines,insertions,deletions,comments,source,status,conf,format")
		for _, r := range rows {
			fmt.Fprintf(file, "%q,%v,%d,%d,%q,%q,%q,%q,%q\n",
				r.filePath, r.hasRedlines, r.insertionCount, r.deletionCount,
				r.comments, r.redlineSource, r.status, r.confidence, r.format)
		}
	}

}

func findPagesFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".pages") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func readFilelist(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var files []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.HasSuffix(line, ".pages") {
			continue
		}
		files = append(files, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return files, nil
}
