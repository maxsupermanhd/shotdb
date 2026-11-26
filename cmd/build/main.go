// Command build bundles the page in web/ into dist/ with esbuild. It does not
// check types: esbuild only strips them. build.sh puts manager.wasm,
// compute.wasm and wasm_exec.js into dist/ first.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	out := flag.String("out", "dist", "output directory")
	minify := flag.Bool("minify", true, "minify the bundles")
	flag.Parse()
	if err := build(*out, *minify); err != nil {
		log.Fatal(err)
	}
}

func build(out string, minify bool) error {
	// app.ts starts the workers as new URL("./manager.js", import.meta.url) and "./compute.js", so the names must stay fixed.
	r := api.Build(api.BuildOptions{
		EntryPointsAdvanced: []api.EntryPoint{
			{InputPath: "web/src/app.ts", OutputPath: "app"},
			{InputPath: "web/src/manager.ts", OutputPath: "manager"},
			{InputPath: "web/src/compute.ts", OutputPath: "compute"},
			{InputPath: "web/style.css", OutputPath: "style"},
		},
		Outdir:            out,
		Bundle:            true,
		Format:            api.FormatESModule,
		Target:            api.ES2022,
		MinifyWhitespace:  minify,
		MinifyIdentifiers: minify,
		MinifySyntax:      minify,
		LogLevel:          api.LogLevelWarning,
		Write:             true,
	})
	if len(r.Errors) > 0 {
		return fmt.Errorf("esbuild: %d errors", len(r.Errors))
	}
	html, err := os.ReadFile("web/index.html")
	if err != nil {
		return err
	}
	const src = `src="./src/app.ts"`
	if !strings.Contains(string(html), src) {
		return errors.New("web/index.html: no " + src)
	}
	page := strings.Replace(string(html), src, `src="./app.js"`, 1)
	return os.WriteFile(filepath.Join(out, "index.html"), []byte(page), 0o644)
}
