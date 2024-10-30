package logfilewriter

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type fileWriter struct {
	dir        string // log file dir
	file       string // log file name, default filepath.Base(os.Args[0])
	sizeLimit  int64  // log file size limit
	archiveDir string // log file archive dir
	compress   bool   // whether compress archived log file
	rotateDays int    // auto rotate days

	rotating int32
	wn       int64
	fp       atomic.Pointer[os.File]
}

type Options interface {
	apply(*fileWriter)
}

type applyFunc func(*fileWriter)

func (f applyFunc) apply(w *fileWriter) {
	f(w)
}

// WithDir sets the log file dir.
func WithDir(dir string) Options {
	return applyFunc(func(fw *fileWriter) {
		fw.dir = dir
	})
}

// WithFileName sets the log file name, default filepath.Base(os.Args[0]).
func WithFileName(filename string) Options {
	return applyFunc(func(fw *fileWriter) {
		fw.file = filename
	})
}

// WithFileSizeLimit sets every log file size limit.
func WithFileSizeLimit(limit int64) Options {
	return applyFunc(func(fw *fileWriter) {
		fw.sizeLimit = limit
	})
}

// WithArchiveDir sets the log file archive dir.
// The log files will be archived to
// "archiveDir/20060102/logfilename.log-20060102-150405".
func WithArchiveDir(dir string) Options {
	return applyFunc(func(fw *fileWriter) {
		fw.archiveDir = dir
	})
}

// WithCompress sets the archived log file compress to gzip format.
// Namely the log files will be archived to
// "archiveDir/20060102/logfilename.log-20060102-150405.gz".
func WithCompress() Options {
	return applyFunc(func(fw *fileWriter) {
		fw.compress = true
	})
}

// WithRotateDays sets the archived log files max rotate days.
func WithRotateDays(days int) Options {
	return applyFunc(func(fw *fileWriter) {
		fw.rotateDays = days
	})
}

// New make a new log file writer with options.
func New(opts ...Options) io.WriteCloser {
	fw := new(fileWriter)
	for _, opt := range opts {
		opt.apply(fw)
	}
	if fw.dir == "" {
		fw.dir, _ = os.Getwd()
	}
	if fw.file == "" {
		name := filepath.Base(os.Args[0])
		if ext := filepath.Ext(name); strings.EqualFold(ext, ".exe") {
			name = strings.TrimSuffix(name, ext)
		}
		fw.file = name
	}

	os.MkdirAll(fw.dir, 0755)
	name := filepath.Join(fw.dir, fw.file) + "-" + time.Now().Format(fileTimeLayout)
	fp, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		fw.fp.Store(fp)
	} else {
		fw.fp.Store(os.Stdout)
	}
	go fw.rotate()
	go fw.autoReplaceFileEveryday()
	return fw
}

const (
	fileTimeLayout    = "20060102-150405"
	archiveDateLayout = "20060102"
)

func (fw *fileWriter) Write(b []byte) (int, error) {
	if fw.sizeLimit > 0 && atomic.AddInt64(&fw.wn, int64(len(b))) > fw.sizeLimit {
		if atomic.CompareAndSwapInt32(&fw.rotating, 0, 1) {
			go fw.replaceAndRotate()
		}
	}
	return fw.fp.Load().Write(b)
}

func (fw *fileWriter) Close() (err error) {
	fp := fw.fp.Load()
	if fp != os.Stdout {
		fw.fp.Store(os.Stdout)
		err = fp.Close()
	}
	return
}

func (fw *fileWriter) replaceAndRotate() {
	defer atomic.StoreInt32(&fw.rotating, 0)

	os.MkdirAll(fw.dir, 0755)
	name := filepath.Join(fw.dir, fw.file) + "-" + time.Now().Format(fileTimeLayout)
	fp, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		time.Sleep(10 * time.Second) // retry intv
		return
	}

	old := fw.fp.Load()
	atomic.StoreInt64(&fw.wn, 0)
	fw.fp.Store(fp)
	if old != os.Stdout {
		time.Sleep(10 * time.Second)
		old.Close()
	}

	fw.rotate()
}

func (fw *fileWriter) rotate() {
	if fw.archiveDir != "" {
		fw.remove()
		fw.archive()
	}
}

func (fw *fileWriter) remove() {
	entries, err := os.ReadDir(fw.archiveDir)
	if err != nil {
		return
	}

	now := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if len(name) != len(archiveDateLayout) {
			continue
		}
		date, err := time.Parse(archiveDateLayout, name)
		if err != nil {
			continue
		}

		days := (now.Unix() - date.Unix()) / (24 * 3600)
		if days <= int64(fw.rotateDays) {
			continue
		}

		fw.removeArchive(name)
	}
}

func (fw *fileWriter) removeArchive(dir string) {
	os.RemoveAll(filepath.Join(fw.archiveDir, dir))
}

func (fw *fileWriter) archive() {
	entries, err := os.ReadDir(fw.dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := filepath.Base(entry.Name())
		if len(name) != len(fw.file)+1+len(fileTimeLayout) { // "filename-20060102-150405"
			continue
		}
		if !strings.HasPrefix(name, fw.file+"-") {
			continue
		}
		t, err := time.Parse(fileTimeLayout, name[len(name)-15:])
		if err != nil {
			continue
		}
		fw.archiveFile(name, t)
	}
}

func (fw *fileWriter) archiveFile(name string, t time.Time) {
	var opened string
	if fp := fw.fp.Load(); fp != nil {
		opened = filepath.Base(fp.Name())
	}
	if name == opened {
		return
	}

	archiveDir := filepath.Join(fw.archiveDir, t.Format(archiveDateLayout))
	err := os.MkdirAll(archiveDir, 0755)
	if err != nil {
		return
	}

	originFile := filepath.Join(fw.dir, name)
	archiveFile := filepath.Join(archiveDir, name)

	if !fw.compress {
		os.Rename(originFile, archiveFile)
		return
	}

	// gzip compress

	src, err := os.Open(originFile)
	if err != nil {
		return
	}
	defer src.Close()

	archiveFile += ".gz"
	dst, err := os.OpenFile(archiveFile, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer dst.Close()

	w := gzip.NewWriter(dst)
	defer w.Close()

	_, err = io.Copy(w, src)
	if err != nil {
		return
	}

	os.Remove(filepath.Join(fw.dir, name))
}

func untilTommorrow() time.Duration {
	now := time.Now()
	tommorrow := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Add(24 * time.Hour)
	return tommorrow.Sub(now)
}

func (fw *fileWriter) autoReplaceFileEveryday() {
	for {
		time.Sleep(untilTommorrow())
		fw.replaceAndRotate()
	}
}
