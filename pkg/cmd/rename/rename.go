package rename

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jenkins-x/jx-gitops/pkg/rootcmd"
	"github.com/jenkins-x/jx-helpers/pkg/cobras/helper"
	"github.com/jenkins-x/jx-helpers/pkg/cobras/templates"
	"github.com/jenkins-x/jx-helpers/pkg/kyamls"
	"github.com/jenkins-x/jx-logging/pkg/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

var (
	splitLong = templates.LongDesc(`
		Renames yaml files to use canonical file names based on the resource name and kind
`)

	splitExample = templates.Examples(`
		# renames files to use a canonical file name
		%s rename --dir .
	`)

	// resourcesSeparator is used to separate multiple objects stored in the same YAML file
	resourcesSeparator = "---\n"
)

// Options the options for the command
type Options struct {
	Dir string
}

// NewCmdRename creates a command object for the command
func NewCmdRename() (*cobra.Command, *Options) {
	o := &Options{}

	cmd := &cobra.Command{
		Use:     "rename",
		Short:   "Renames yaml files to use canonical file names based on the resource name and kind",
		Long:    splitLong,
		Example: fmt.Sprintf(splitExample, rootcmd.BinaryName),
		Run: func(cmd *cobra.Command, args []string) {
			err := o.Run()
			helper.CheckErr(err)
		},
	}
	cmd.Flags().StringVarP(&o.Dir, "dir", "d", ".", "the directory to recursively look for the *.yaml or *.yml files")
	return cmd, o
}

// Run implements the command
func (o *Options) Run() error {
	err := filepath.Walk(o.Dir, func(path string, info os.FileInfo, err error) error {
		if info == nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}

		node, err := yaml.ReadFile(path)
		if err != nil {
			return errors.Wrapf(err, "failed to load file %s", path)
		}

		name := kyamls.GetName(node, path)
		if name == "" {
			log.Logger().Warnf("no name for file %s so ignoring", path)
			return nil
		}

		kind := kyamls.GetKind(node, path)

		dir, file := filepath.Split(path)
		ext := filepath.Ext(path)

		cn := o.canonicalName(kind, name)

		newFile := cn + ext
		newPath := filepath.Join(dir, newFile)

		if newPath != path {
			log.Logger().Infof("renaming %s => %s", file, newFile)
			err = os.Rename(path, newPath)
			if err != nil {
				return errors.Wrapf(err, "failed to rename %s to %s", file, newFile)
			}

		}
		return nil
	})
	if err != nil {
		return errors.Wrapf(err, "failed to rename YAML files in dir %s", o.Dir)
	}
	return nil
}

var (
	kindSuffixes = map[string]string{
		"clusterrolebinding":             "crb",
		"configmap":                      "cm",
		"customresourcedefinition":       "crd",
		"deployment":                     "deploy",
		"mutatingwebhookconfiguration":   "mutwebhookcfg",
		"namespace":                      "ns",
		"rolebinding":                    "rb",
		"service":                        "svc",
		"serviceaccount":                 "sa",
		"validatingwebhookconfiguration": "valwebhookcfg",
	}
)

func (o *Options) canonicalName(kind string, name string) string {
	lk := strings.ToLower(kind)
	suffix := kindSuffixes[lk]
	if suffix == "" {
		suffix = lk
	}
	if kind == "" {
		return name
	}
	return name + "-" + suffix
}
