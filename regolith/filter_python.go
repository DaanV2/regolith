package regolith

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type PythonFilterDefinition struct {
	FilterDefinition
	Script   string `json:"script,omitempty"`
	VenvSlot int    `json:"venvSlot,omitempty"`
}

type PythonFilter struct {
	Filter
	Definition PythonFilterDefinition `json:"-"`
}

func PythonFilterDefinitionFromObject(id string, obj map[string]interface{}) (*PythonFilterDefinition, error) {
	filter := &PythonFilterDefinition{FilterDefinition: *FilterDefinitionFromObject(id)}
	scripObj, ok := obj["script"]
	if !ok {
		return nil, WrappedErrorf(jsonPropertyMissingError, "script")
	}
	script, ok := scripObj.(string)
	if !ok {
		return nil, WrappedErrorf(jsonPropertyTypeError, "script", "string")
	}
	filter.Script = script
	filter.VenvSlot, _ = obj["venvSlot"].(int) // default venvSlot is 0
	return filter, nil
}

func (f *PythonFilter) run(context RunContext) error {
	// Run filter
	pythonCommand, err := findPython()
	if err != nil {
		return PassError(err)
	}
	scriptPath := filepath.Join(context.AbsoluteLocation, f.Definition.Script)
	if needsVenv(filepath.Dir(scriptPath)) {
		venvPath, err := f.Definition.resolveVenvPath(context.DotRegolithPath)
		if err != nil {
			return WrapError(err, "Failed to resolve venv path.")
		}
		Logger.Debug("Running Python filter using venv: ", venvPath)
		pythonCommand = filepath.Join(
			venvPath, venvScriptsPath, "python"+exeSuffix)
	}
	var args []string
	if len(f.Settings) == 0 {
		args = append([]string{"-u", scriptPath}, f.Arguments...)
	} else {
		jsonSettings, _ := json.Marshal(f.Settings)
		args = append(
			[]string{"-u", scriptPath, string(jsonSettings)},
			f.Arguments...,
		)
	}
	err = RunSubProcess(
		pythonCommand, args, context.AbsoluteLocation,
		GetAbsoluteWorkingDirectory(context.DotRegolithPath),
		ShortFilterName(f.Id))
	if err != nil {
		return WrapError(err, "Failed to run Python script.")
	}
	return nil
}

func (f *PythonFilter) Run(context RunContext) (bool, error) {
	if err := f.run(context); err != nil {
		return false, PassError(err)
	}
	return context.IsInterrupted(), nil
}

func (f *PythonFilterDefinition) CreateFilterRunner(runConfiguration map[string]interface{}) (FilterRunner, error) {
	basicFilter, err := filterFromObject(runConfiguration)
	if err != nil {
		return nil, WrapError(err, filterFromObjectError)
	}
	filter := &PythonFilter{
		Filter:     *basicFilter,
		Definition: *f,
	}
	return filter, nil
}

func (f *PythonFilterDefinition) InstallDependencies(
	parent *RemoteFilterDefinition, dotRegolithPath string,
) error {
	installLocation := ""
	// Install dependencies
	if parent != nil {
		installLocation = parent.GetDownloadPath(dotRegolithPath)
	}
	Logger.Infof("Downloading dependencies for %s...", f.Id)
	joinedPath := filepath.Join(installLocation, f.Script)
	scriptPath, err := filepath.Abs(joinedPath)
	if err != nil {
		return WrapErrorf(err, filepathAbsError, joinedPath)
	}

	// Install the filter dependencies
	filterPath := filepath.Dir(scriptPath)
	if needsVenv(filterPath) {
		venvPath, err := f.resolveVenvPath(dotRegolithPath)
		if err != nil {
			return WrapError(err, "Failed to resolve venv path.")
		}
		Logger.Info("Creating venv...")
		pythonCommand, err := findPython()
		if err != nil {
			return PassError(err)
		}
		// Create the "venv"
		err = RunSubProcess(
			pythonCommand, []string{"-m", "venv", venvPath}, filterPath, "", ShortFilterName(f.Id))
		if err != nil {
			return WrapError(err, "Failed to create venv.")
		}
		// Update pip of the venv
		venvPythonCommand := filepath.Join(
			venvPath, venvScriptsPath, "python"+exeSuffix)
		err = RunSubProcess(
			venvPythonCommand,
			[]string{"-m", "pip", "install", "--upgrade", "pip"},
			filterPath, "", ShortFilterName(f.Id))
		if err != nil {
			Logger.Warn("Failed to upgrade pip in venv.")
		}
		Logger.Info("Installing pip dependencies...")
		err = RunSubProcess(
			filepath.Join(venvPath, venvScriptsPath, "pip"+exeSuffix),
			[]string{"install", "-r", "requirements.txt"}, filterPath, filterPath, ShortFilterName(f.Id))
		if err != nil {
			return WrapErrorf(
				err, "Couldn't run Pip to install dependencies of %s",
				f.Id,
			)
		}
	}
	Logger.Infof("Dependencies for %s installed successfully.", f.Id)
	return nil
}

func (f *PythonFilterDefinition) Check(context RunContext) error {
	pythonCommand, err := findPython()
	if err != nil {
		return PassError(err)
	}
	cmd, err := exec.Command(pythonCommand, "--version").Output()
	if err != nil {
		return WrapError(err, "Python version check failed.")
	}
	a := strings.TrimPrefix(strings.Trim(string(cmd), " \n\t"), "Python ")
	Logger.Debugf("Found Python version %s", a)
	return nil
}

func (f *PythonFilter) Check(context RunContext) error {
	return f.Definition.Check(context)
}

func (f *PythonFilter) CopyArguments(parent *RemoteFilter) {
	f.Arguments = append(f.Arguments, parent.Arguments...)
	f.Settings = parent.Settings
	f.Definition.VenvSlot = parent.Definition.VenvSlot
}

func (f *PythonFilterDefinition) resolveVenvPath(dotRegolithPath string) (string, error) {
	resolvedPath, err := filepath.Abs(
		filepath.Join(filepath.Join(dotRegolithPath, "cache/venvs"), strconv.Itoa(f.VenvSlot)))
	if err != nil {
		return "", WrapErrorf(
			err, "Unable to create venv for VenvSlot %v.", f.VenvSlot)
	}
	return resolvedPath, nil
}

func needsVenv(filterPath string) bool {
	stats, err := os.Stat(filepath.Join(filterPath, "requirements.txt"))
	if err == nil {
		return !stats.IsDir()
	}
	return false
}

func findPython() (string, error) {
	var err error
	for _, c := range []string{"python3", "python"} {
		_, err = exec.LookPath(c)
		if err == nil {
			return c, nil
		}
	}
	return "", WrappedError(
		"Python not found, download and install it from " +
			"https://www.python.org/downloads/")
}
