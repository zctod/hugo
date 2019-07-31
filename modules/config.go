// Copyright 2019 The Hugo Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package modules

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gohugoio/hugo/common/hugo"

	"github.com/gohugoio/hugo/config"
	"github.com/gohugoio/hugo/hugofs/files"
	"github.com/gohugoio/hugo/langs"
	"github.com/mitchellh/mapstructure"
)

var DefaultModuleConfig = Config{

	// Default to direct, which means "git clone" and similar. We
	// will investigate proxy settings in more depth later.
	// See https://github.com/golang/go/issues/26334
	Proxy: "direct",

	// Comma separated glob list matching paths that should not use the
	// proxy configured above.
	NoProxy: "none",

	// Comma separated glob list matching paths that should be
	// treated as private.
	Private: "*.*",
}

// ApplyProjectConfigDefaults applies default/missing module configuration for
// the main project.
func ApplyProjectConfigDefaults(cfg config.Provider, mod Module) error {
	moda := mod.(*moduleAdapter)

	// Map legacy directory config into the new module.
	languages := cfg.Get("languagesSortedDefaultFirst").(langs.Languages)
	isMultiHost := languages.IsMultihost()

	// To bridge between old and new configuration format we need
	// a way to make sure all of the core components are configured on
	// the basic level.
	componentsConfigured := make(map[string]bool)
	for _, mnt := range moda.mounts {
		componentsConfigured[mnt.Component()] = true
	}

	type dirKeyComponent struct {
		key          string
		component    string
		multilingual bool
	}

	dirKeys := []dirKeyComponent{
		{"contentDir", files.ComponentFolderContent, true},
		{"dataDir", files.ComponentFolderData, false},
		{"layoutDir", files.ComponentFolderLayouts, false},
		{"i18nDir", files.ComponentFolderI18n, false},
		{"archetypeDir", files.ComponentFolderArchetypes, false},
		{"assetDir", files.ComponentFolderAssets, false},
		{"", files.ComponentFolderStatic, isMultiHost},
	}

	createMountsFor := func(d dirKeyComponent, cfg config.Provider) []Mount {
		var lang string
		if language, ok := cfg.(*langs.Language); ok {
			lang = language.Lang
		}

		// Static mounts are a little special.
		if d.component == files.ComponentFolderStatic {
			var mounts []Mount
			staticDirs := getStaticDirs(cfg)
			if len(staticDirs) > 0 {
				componentsConfigured[d.component] = true
			}

			for _, dir := range staticDirs {
				mounts = append(mounts, Mount{Lang: lang, Source: dir, Target: d.component})
			}

			return mounts

		}

		if cfg.IsSet(d.key) {
			source := cfg.GetString(d.key)
			componentsConfigured[d.component] = true

			return []Mount{Mount{
				// No lang set for layouts etc.
				Source: source,
				Target: d.component}}
		}

		return nil
	}

	createMounts := func(d dirKeyComponent) []Mount {
		var mounts []Mount
		if d.multilingual {
			if d.component == files.ComponentFolderContent {
				seen := make(map[string]bool)
				hasContentDir := false
				for _, language := range languages {
					if language.ContentDir != "" {
						hasContentDir = true
						break
					}
				}

				if hasContentDir {
					for _, language := range languages {
						contentDir := language.ContentDir
						if contentDir == "" {
							contentDir = files.ComponentFolderContent
						}
						if contentDir == "" || seen[contentDir] {
							continue
						}
						seen[contentDir] = true
						mounts = append(mounts, Mount{Lang: language.Lang, Source: contentDir, Target: d.component})
					}
				}

				componentsConfigured[d.component] = len(seen) > 0

			} else {
				for _, language := range languages {
					mounts = append(mounts, createMountsFor(d, language)...)
				}
			}
		} else {
			mounts = append(mounts, createMountsFor(d, cfg)...)
		}

		return mounts
	}

	var mounts []Mount
	for _, dirKey := range dirKeys {
		if componentsConfigured[dirKey.component] {

			continue
		}

		mounts = append(mounts, createMounts(dirKey)...)

	}

	// Add default configuration
	for _, dirKey := range dirKeys {
		if componentsConfigured[dirKey.component] {
			continue
		}
		mounts = append(mounts, Mount{Source: dirKey.component, Target: dirKey.component})
	}

	// Prepend the mounts from configuration.
	mounts = append(moda.mounts, mounts...)

	moda.mounts = mounts

	return nil
}

// DecodeConfig creates a modules Config from a given Hugo configuration.
func DecodeConfig(cfg config.Provider) (Config, error) {
	c := DefaultModuleConfig

	if cfg == nil {
		return c, nil
	}

	themeSet := cfg.IsSet("theme")
	moduleSet := cfg.IsSet("module")

	if moduleSet {
		m := cfg.GetStringMap("module")
		if err := mapstructure.WeakDecode(m, &c); err != nil {
			return c, err
		}

		for i, mnt := range c.Mounts {
			mnt.Source = filepath.Clean(mnt.Source)
			mnt.Target = filepath.Clean(mnt.Target)
			c.Mounts[i] = mnt
		}

	}

	if themeSet {
		imports := config.GetStringSlicePreserveString(cfg, "theme")
		for _, imp := range imports {
			c.Imports = append(c.Imports, Import{
				Path: imp,
			})
		}

	}

	return c, nil
}

// Config holds a module config.
type Config struct {
	Mounts  []Mount
	Imports []Import

	// Meta info about this module (license information etc.).
	Params map[string]interface{}

	// Will be validated against the running Hugo version.
	HugoVersion HugoVersion

	// Configures GOPROXY.
	Proxy string
	// Configures GONOPROXY.
	NoProxy string
	// Configures GOPRIVATE.
	Private string
}

// HugoVersion holds Hugo binary version requirements for a module.
type HugoVersion struct {
	// The minimum Hugo version that this module works with.
	Min hugo.VersionString

	// The maxium Hugo version that this module works with.
	Max hugo.VersionString

	// Set if the extended version is needed.
	Extended bool
}

func (v HugoVersion) String() string {
	extended := ""
	if v.Extended {
		extended = " extended"
	}

	if v.Min != "" && v.Max != "" {
		return fmt.Sprintf("%s/%s%s", v.Min, v.Max, extended)
	}

	if v.Min != "" {
		return fmt.Sprintf("Min %s%s", v.Min, extended)
	}

	if v.Max != "" {
		return fmt.Sprintf("Max %s%s", v.Max, extended)
	}

	return extended
}

// IsValid reports whether this version is valid compared to the running
// Hugo binary.
func (v HugoVersion) IsValid() bool {
	current := hugo.CurrentVersion.Version()
	if v.Extended && !hugo.IsExtended {
		return false
	}

	isValid := true

	if v.Min != "" && current.Compare(v.Min) > 0 {
		isValid = false
	}

	if v.Max != "" && current.Compare(v.Max) < 0 {
		isValid = false
	}

	return isValid
}

type Import struct {
	Path         string // Module path
	IgnoreConfig bool   // Ignore any config.toml found.
	Disable      bool   // Turn off this module.
	Mounts       []Mount
}

type Mount struct {
	Source string // relative path in source repo, e.g. "scss"
	Target string // relative target path, e.g. "assets/bootstrap/scss"

	Lang string // any language code associated with this mount.
}

func (m Mount) Component() string {
	return strings.Split(m.Target, fileSeparator)[0]
}

func getStaticDirs(cfg config.Provider) []string {
	var staticDirs []string
	for i := -1; i <= 10; i++ {
		staticDirs = append(staticDirs, getStringOrStringSlice(cfg, "staticDir", i)...)
	}
	return staticDirs
}

func getStringOrStringSlice(cfg config.Provider, key string, id int) []string {

	if id >= 0 {
		key = fmt.Sprintf("%s%d", key, id)
	}

	return config.GetStringSlicePreserveString(cfg, key)

}