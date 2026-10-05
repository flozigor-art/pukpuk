package node

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

// Export rebuilds a human-readable folder tree from a database backup and the
// blobs of this node. Files are hard-linked when possible (no extra space),
// otherwise copied. This is the escape hatch for the content-addressed store:
//
//	crm-node export [--db backups/crm-....db] [--out export] [--copy]
func Export(dataDir, dbPath, outDir string, copyFiles bool) error {
	if dbPath == "" {
		dbs, _ := filepath.Glob(filepath.Join(dataDir, "backups", "crm-*.db"))
		if len(dbs) == 0 {
			return fmt.Errorf("no database backups in %s/backups — pass --db", dataDir)
		}
		sort.Strings(dbs)
		dbPath = dbs[len(dbs)-1]
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	prefix := "LP"
	db.QueryRow(`SELECT value FROM settings WHERE key = 'code_prefix'`).Scan(&prefix)

	kinds := map[string]string{}
	rows, err := db.Query(`SELECT key, name FROM asset_kinds`)
	if err != nil {
		return fmt.Errorf("read %s: %w", dbPath, err)
	}
	for rows.Next() {
		var k, n string
		rows.Scan(&k, &n)
		kinds[k] = n
	}
	rows.Close()

	type item struct{ sha, rel string }
	var items []item
	rows, err = db.Query(`SELECT v.num, v.title, COALESCE(va.language_code, ''), a.kind, a.filename, a.version, a.sha256
		FROM assets a JOIN videos v ON v.id = a.video_id LEFT JOIN variants va ON va.id = a.variant_id
		WHERE a.deleted_at IS NULL AND v.deleted_at IS NULL ORDER BY v.num, a.id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var num int64
		var title, lang, kind, filename, sha string
		var version int
		if err := rows.Scan(&num, &title, &lang, &kind, &filename, &version, &sha); err != nil {
			rows.Close()
			return err
		}
		if lang == "" {
			lang = "общие"
		}
		kn := kinds[kind]
		if kn == "" {
			kn = kind
		}
		ext := filepath.Ext(filename)
		name := strings.TrimSuffix(filename, ext)
		if version > 1 {
			name += fmt.Sprintf(" (v%d)", version)
		}
		rel := filepath.Join("Ролики", clean(fmt.Sprintf("%s-%04d %s", prefix, num, title)), lang, clean(kn), clean(name+ext))
		items = append(items, item{sha, rel})
	}
	rows.Close()

	rows, err = db.Query(`SELECT title, artist, filename, sha256 FROM tracks WHERE deleted_at IS NULL ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var title, artist, filename, sha string
		rows.Scan(&title, &artist, &filename, &sha)
		name := title
		if artist != "" {
			name = artist + " - " + title
		}
		items = append(items, item{sha, filepath.Join("Музыка", clean(name+filepath.Ext(filename)))})
	}
	rows.Close()

	seen := map[string]bool{}
	var done, missing int
	for _, it := range items {
		dst := filepath.Join(outDir, it.rel)
		for i := 2; seen[dst]; i++ {
			ext := filepath.Ext(it.rel)
			dst = filepath.Join(outDir, strings.TrimSuffix(it.rel, ext)+fmt.Sprintf(" [%d]", i)+ext)
		}
		seen[dst] = true
		src := filepath.Join(dataDir, "blobs", it.sha[:2], it.sha)
		if _, err := os.Stat(src); err != nil {
			fmt.Fprintf(os.Stderr, "missing: %s (%s)\n", it.rel, it.sha[:12])
			missing++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		os.Remove(dst)
		if copyFiles || os.Link(src, dst) != nil {
			if err := copyFile(src, dst); err != nil {
				return err
			}
		}
		done++
	}
	fmt.Printf("exported %d files to %s (from %s)", done, outDir, filepath.Base(dbPath))
	if missing > 0 {
		fmt.Printf(", %d missing", missing)
	}
	fmt.Println()
	return nil
}

func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, s)
	s = strings.TrimSpace(strings.TrimRight(s, ". "))
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120])
	}
	if s == "" {
		s = "_"
	}
	return s
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Verify re-hashes every stored blob and reports corrupted files.
func Verify(dataDir string) error {
	var ok, bad int
	err := filepath.WalkDir(filepath.Join(dataDir, "blobs"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		sum, err := hashFile(p)
		if err != nil {
			return err
		}
		if sum != d.Name() {
			fmt.Printf("CORRUPTED %s\n", p)
			bad++
		} else {
			ok++
		}
		return nil
	})
	fmt.Printf("verified %d files, %d corrupted\n", ok+bad, bad)
	if bad > 0 {
		return fmt.Errorf("%d corrupted files", bad)
	}
	return err
}
