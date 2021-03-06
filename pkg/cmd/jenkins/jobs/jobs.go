package jobs

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/Masterminds/sprig"
	"github.com/jenkins-x/jx-gitops/pkg/apis/gitops/v1alpha1"
	"github.com/jenkins-x/jx-gitops/pkg/rootcmd"
	"github.com/jenkins-x/jx-gitops/pkg/sourceconfigs"
	"github.com/jenkins-x/jx-helpers/v3/pkg/cobras/helper"
	"github.com/jenkins-x/jx-helpers/v3/pkg/cobras/templates"
	"github.com/jenkins-x/jx-helpers/v3/pkg/files"
	"github.com/jenkins-x/jx-helpers/v3/pkg/templater"
	"github.com/jenkins-x/jx-helpers/v3/pkg/termcolor"
	"github.com/jenkins-x/jx-helpers/v3/pkg/yamls"
	"github.com/jenkins-x/jx-logging/v3/pkg/log"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

var (
	info = termcolor.ColorInfo

	cmdLong = templates.LongDesc(`
		Generates the Jenkins Jobs helm files
`)

	cmdExample = templates.Examples(`
		# generate the jenkins job files
		%s jenkins jobs

	`)
)

// LabelOptions the options for the command
type Options struct {
	Dir                string
	ConfigFile         string
	OutDir             string
	DefaultXmlTemplate string
	SourceConfig       v1alpha1.SourceConfig
	JenkinsServers     map[string][]*JenkinsTemplateConfig
}

// JenkinsTemplateConfig stores the data to render jenkins config files
type JenkinsTemplateConfig struct {
	Server          string
	Key             string
	XMLTemplateFile string
	XMLTemplateText string
	TemplateData    map[string]interface{}
}

// NewCmdJenkinsJobs creates a command object for the command
func NewCmdJenkinsJobs() (*cobra.Command, *Options) {
	o := &Options{}

	cmd := &cobra.Command{
		Use:     "jobs",
		Aliases: []string{"job"},
		Short:   "Generates the Jenkins Jobs helm files",
		Long:    cmdLong,
		Example: fmt.Sprintf(cmdExample, rootcmd.BinaryName),
		Run: func(cmd *cobra.Command, args []string) {
			err := o.Run()
			helper.CheckErr(err)
		},
	}
	cmd.Flags().StringVarP(&o.Dir, "dir", "d", ".", "the current working directory")
	cmd.Flags().StringVarP(&o.OutDir, "out", "o", "", "the output directory for the generated config files. If not specified defaults to the jenkins dir in the current directory")
	cmd.Flags().StringVarP(&o.ConfigFile, "config", "c", "", "the configuration file to load for the repository configurations. If not specified we look in ./.jx/gitops/source-config.yaml")
	cmd.Flags().StringVarP(&o.DefaultXmlTemplate, "default-xml-template", "", "", "the default XML template file if none is configured for a repository")
	return cmd, o
}

func (o *Options) Validate() error {
	if o.ConfigFile == "" {
		o.ConfigFile = filepath.Join(o.Dir, ".jx", "gitops", v1alpha1.SourceConfigFileName)
	}
	if o.OutDir == "" {
		o.OutDir = filepath.Join(o.Dir, "jenkins")
	}

	exists, err := files.FileExists(o.ConfigFile)
	if err != nil {
		return errors.Wrapf(err, "failed to check if file exists %s", o.ConfigFile)
	}
	if !exists {
		log.Logger().Infof("the source config file %s does not exist", info(o.ConfigFile))
		return nil
	}

	if o.DefaultXmlTemplate != "" {
		exists, err := files.FileExists(o.DefaultXmlTemplate)
		if err != nil {
			return errors.Wrapf(err, "failed to check if file exists %s", o.DefaultXmlTemplate)
		}
		if !exists {
			return errors.Errorf("the default-xml-template file %s does not exist", o.DefaultXmlTemplate)
		}
	}

	err = yamls.LoadFile(o.ConfigFile, &o.SourceConfig)
	if err != nil {
		return errors.Wrapf(err, "failed to load file %s", o.ConfigFile)
	}

	if o.JenkinsServers == nil {
		o.JenkinsServers = map[string][]*JenkinsTemplateConfig{}
	}
	return nil
}

func (o *Options) Run() error {
	err := o.Validate()
	if err != nil {
		return errors.Wrapf(err, "failed to validate options")
	}

	config := &o.SourceConfig
	for i := range config.Spec.Groups {
		group := &config.Spec.Groups[i]
		for j := range group.Repositories {
			repo := &group.Repositories[j]
			sourceconfigs.DefaultValues(config, group, repo)
			if repo.Jenkins == nil {
				continue
			}
			err = o.processJenkinsConfig(group, repo, repo.Jenkins)
			if err != nil {
				return errors.Wrapf(err, "failed to process Jenkins Config")
			}
		}
	}

	for server, configs := range o.JenkinsServers {
		dir := filepath.Join(o.OutDir, server)
		err = os.MkdirAll(dir, files.DefaultDirWritePermissions)
		if err != nil {
			return errors.Wrapf(err, "failed to create dir %s", dir)
		}
		path := filepath.Join(dir, "values.yaml")
		log.Logger().Infof("creating Jenkins values.yaml file %s", path)

		funcMap := sprig.TxtFuncMap()

		jobs := map[string]interface{}{}

		for _, jcfg := range configs {
			output, err := templater.Evaluate(funcMap, jcfg.TemplateData, jcfg.XMLTemplateText, jcfg.XMLTemplateFile, "Jenkins Server "+server)
			if err != nil {
				return errors.Wrapf(err, "failed to evaluate template %s", jcfg.XMLTemplateFile)
			}
			jobs[jcfg.Key] = output
		}

		values := map[string]interface{}{
			"master": map[string]interface{}{
				"jobs": jobs,
			},
		}

		data, err := yaml.Marshal(values)
		if err != nil {
			return errors.Wrapf(err, "failed to marshal values YAML for server %s", server)
		}

		err = ioutil.WriteFile(path, data, files.DefaultFileWritePermissions)
		if err != nil {
			return errors.Wrapf(err, "failed to save file %s", path)
		}
	}

	return nil
}

func (o *Options) processJenkinsConfig(group *v1alpha1.RepositoryGroup, repo *v1alpha1.Repository, jc *v1alpha1.JenkinsConfig) error {
	server := jc.Server
	if server == "" {
		log.Logger().Infof("ignoring repository %s as it has no Jenkins server defined", repo.URL)
		return nil
	}
	xmlTemplate := o.DefaultXmlTemplate
	if jc.XmlTemplate != "" {
		xmlTemplate = filepath.Join(o.Dir, jc.XmlTemplate)
		exists, err := files.FileExists(xmlTemplate)
		if err != nil {
			return errors.Wrapf(err, "failed to check if file exists %s", xmlTemplate)
		}
		if !exists {
			return errors.Errorf("the xmlTemplate file %s does not exist", xmlTemplate)
		}
	}
	if xmlTemplate == "" {
		log.Logger().Infof("ignoring repository %s as it has no Jenkins server defined", repo.URL)
		return nil
	}

	data, err := ioutil.ReadFile(xmlTemplate)
	if err != nil {
		return errors.Wrapf(err, "failed to load file %s", xmlTemplate)
	}

	templateData := map[string]interface{}{
		"Owner":        group.Owner,
		"GitServerURL": group.Provider,
		"GitKind":      group.ProviderKind,
		"GitName":      group.ProviderName,
		"Repository":   repo.Name,
		"URL":          repo.URL,
		"CloneURL":     repo.HTTPCloneURL,
	}

	o.JenkinsServers[server] = append(o.JenkinsServers[server], &JenkinsTemplateConfig{
		Server:          server,
		Key:             repo.Name,
		XMLTemplateFile: xmlTemplate,
		XMLTemplateText: string(data),
		TemplateData:    templateData,
	})
	return nil
}
