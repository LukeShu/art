/*******************************************************************************
*
* Copyright 2017 Stefan Majewsky <majewsky@gmx.net>
*
* This program is free software: you can redistribute it and/or modify it under
* the terms of the GNU General Public License as published by the Free Software
* Foundation, either version 3 of the License, or (at your option) any later
* version.
*
* This program is distributed in the hope that it will be useful, but WITHOUT ANY
* WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR
* A PARTICULAR PURPOSE. See the GNU General Public License for more details.
*
* You should have received a copy of the GNU General Public License along with
* this program. If not, see <http://www.gnu.org/licenses/>.
*
*******************************************************************************/

package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//Repository represents a directory containing package files.
type Repository struct {
	Name string `toml:"name"`
	Path string `toml:"path"`
}

//FileName returns the basename of the repository metadata archive.
func (r Repository) FileName() string {
	return r.Name + ".db.tar.xz"
}

//RepositoryEntry represents an entry for a package in a repo metadata archive.
type RepositoryEntry struct {
	PackageName string
	FileName    string
	MD5Digest   string
}

func (r Repository) readMetadata() ([]RepositoryEntry, error) {
	metadataPath := filepath.Join(r.Path, r.FileName())
	file, err := os.Open(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	cmd := exec.Command("xz", "--decompress", "--stdout")
	cmd.Stdin = file
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return nil, err
	}

	tr := tar.NewReader(&buf)
	var result []RepositoryEntry
	for {
		hdr, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("error reading %s: %s", metadataPath, err.Error())
		}

		ok, entry, err := readMetadataEntry(hdr, tr)
		if err != nil {
			return nil, fmt.Errorf("error reading %s: %s", metadataPath, err.Error())
		}
		if ok {
			result = append(result, entry)
		}
	}
	return result, nil
}

func readMetadataEntry(h *tar.Header, r io.Reader) (ok bool, entry RepositoryEntry, err error) {
	//entries are regular files like */desc
	if !h.FileInfo().Mode().IsRegular() {
		ok = false
		return
	}
	if filepath.Base(h.Name) != "desc" {
		ok = false
		return
	}

	var buf []byte
	buf, err = ioutil.ReadAll(r)
	if err != nil {
		ok = false
		return
	}

	//read line by line
	currentField := ""
	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "%") && strings.HasSuffix(line, "%") {
			currentField = line
			continue
		}

		switch currentField {
		case "%NAME%":
			entry.PackageName = line
		case "%FILENAME%":
			entry.FileName = line
		case "%MD5SUM%":
			entry.MD5Digest = line
		}
	}
	ok = true
	return
}

func (r Repository) addNewPackages(allOutputFiles []string) (ok bool) {
	progress("Adding new packages to repository")

	//get existing entries
	entries, err := r.readMetadata()
	if err != nil {
		showError(err)
		return false
	}
	entryByFilename := make(map[string]RepositoryEntry, len(entries))
	for _, entry := range entries {
		entryByFilename[entry.FileName] = entry
	}

	//which files need to be added?
	var newOutputFiles []string
	for _, fileName := range allOutputFiles {
		step()
		entry, exists := entryByFilename[fileName]
		if !exists {
			newOutputFiles = append(newOutputFiles, fileName)
			continue
		}

		buf, err := ioutil.ReadFile(filepath.Join(r.Path, fileName))
		if err != nil {
			showError(err)
			return false
		}
		if entry.MD5Digest != md5digest(buf) {
			newOutputFiles = append(newOutputFiles, fileName)
		}
	}
	done()

	if len(newOutputFiles) == 0 {
		return true
	}

	cmd := exec.Command("repo-add", append([]string{"-R", r.FileName()}, newOutputFiles...)...)
	cmd.Dir = r.Path
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	showError(err)
	return err == nil
}

func (r Repository) pruneMetadata(allOutputFiles []string) (ok bool) {
	progress("Removing old entries from repo metadata...\n")

	//get existing entries
	entries, err := r.readMetadata()
	if err != nil {
		showError(err)
		return false
	}

	//collect all entries that do not match a current output file
	isOutputFile := make(map[string]bool, len(allOutputFiles))
	for _, fileName := range allOutputFiles {
		isOutputFile[fileName] = true
	}
	var entriesToDelete []string
	for _, entry := range entries {
		if !isOutputFile[entry.FileName] {
			entriesToDelete = append(entriesToDelete, entry.PackageName)
		}
	}

	if len(entriesToDelete) == 0 {
		return true
	}

	cmd := exec.Command("repo-remove", append([]string{r.FileName()}, entriesToDelete...)...)
	cmd.Dir = r.Path
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	showError(err)
	return err == nil
}

func (r Repository) prunePackages(allOutputFiles []string) (ok bool) {
	progress("Removing old packages from target directory")

	isOutputFile := make(map[string]bool, len(allOutputFiles))
	for _, fileName := range allOutputFiles {
		isOutputFile[fileName] = true
	}

	dir, err := os.Open(r.Path)
	if err != nil {
		showError(err)
		return false
	}
	names, err := dir.Readdirnames(-1)
	if err != nil {
		showError(err)
		return false
	}

	ok = true
	for _, fileName := range names {
		if !strings.HasSuffix(fileName, ".pkg.tar.xz") {
			continue
		}
		step()

		if !isOutputFile[fileName] {
			err := os.Remove(filepath.Join(r.Path, fileName))
			if err != nil {
				showError(err)
				ok = false
			}
		}
	}

	done()
	return
}
