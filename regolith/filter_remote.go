package regolith

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/hashicorp/go-getter"
	"github.com/otiai10/copy"
)

type RemoteFilterDefinition struct {
	FilterDefinition
	Url     string `json:"url,omitempty"`
	Version string `json:"version,omitempty"`
	// RemoteFilters can propagate some of the properties unique to other types
	// of filers (like Python's venvSlot).
	VenvSlot int `json:"venvSlot,omitempty"`
}

type RemoteFilter struct {
	Filter
	Definition RemoteFilterDefinition `json:"-"`
}

func RemoteFilterDefinitionFromObject(id string, obj map[string]interface{}) (*RemoteFilterDefinition, error) {
	result := &RemoteFilterDefinition{FilterDefinition: *FilterDefinitionFromObject(id)}
	url, ok := obj["url"].(string)
	if !ok {
		result.Url = StandardLibraryUrl
	} else {
		result.Url = url
	}
	version, ok := obj["version"].(string)
	if !ok {
		return nil, WrapErrorf(
			nil, "missing 'version' property in filter definition %s", id)
	}
	result.Version = version
	result.VenvSlot, _ = obj["venvSlot"].(int) // default venvSlot is 0
	return result, nil
}

func (f *RemoteFilter) Run(absoluteLocation string) error {
	// Disabled filters are skipped
	if f.Disabled {
		Logger.Infof("Filter '%s' is disabled, skipping.", f.GetFriendlyName())
		return nil
	}
	// All other filters require safe mode to be turned off
	if f.Definition.Url != StandardLibraryUrl && !IsUnlocked() {
		return WrapError(
			nil,
			"Safe mode is on, which protects you from potentially unsafe "+
				"code.\nYou may turn it off using 'regolith unlock'",
		)
	}
	Logger.Infof("Running filter %s", f.GetFriendlyName())
	start := time.Now()
	defer Logger.Debugf("Executed in %s", time.Since(start))

	Logger.Debugf("RunRemoteFilter '%s'", f.Definition.Url)
	if !f.IsCached() {
		return WrapError(
			nil, "Filter is not downloaded. Please run 'regolith install'")
	}

	path := f.GetDownloadPath()
	absolutePath, _ := filepath.Abs(path)
	filterCollection, err := f.SubfilterCollection()
	if err != nil {
		return err
	}
	for _, filter := range filterCollection.Filters {
		// Overwrite the venvSlot with the parent value
		err := filter.Run(absolutePath)
		if err != nil {
			return err
		}
	}
	return nil
}

func (f *RemoteFilterDefinition) CreateFilterRunner(runConfiguration map[string]interface{}) (FilterRunner, error) {
	basicFilter, err := FilterFromObject(runConfiguration)
	if err != nil {
		return nil, WrapError(err, "failed to create Java filter")
	}
	filter := &RemoteFilter{
		Filter:     *basicFilter,
		Definition: *f,
	}
	return filter, nil
}

// TODO - this code is almost a duplicate of the code in the
// (f *RemoteFilter) SubfilterCollection()
func (f *RemoteFilterDefinition) InstallDependencies(parent *RemoteFilterDefinition) error {
	path := filepath.Join(f.GetDownloadPath(), "filter.json")
	file, err := ioutil.ReadFile(path)

	if err != nil {
		return WrapErrorf(err, "couldn't read %q", path)
	}

	var filterCollection map[string]interface{}
	err = json.Unmarshal(file, &filterCollection)
	if err != nil {
		return WrapErrorf(
			err, "couldn't load %s! Does the file contain correct json?", path)
	}
	// Filters
	filters, ok := filterCollection["filters"].([]interface{})
	if !ok {
		return WrapErrorf(nil, "could not parse filters of %q", path)
	}
	for i, filter := range filters {
		filter, ok := filter.(map[string]interface{})
		if !ok {
			return WrapErrorf(nil, "could not parse filter %v of %q", i, path)
		}
		filterInstaller, err := FilterInstallerFromObject(
			fmt.Sprintf("%v:subfilter%v", f.Id, i), filter)
		if err != nil {
			return WrapErrorf(
				err, "could not parse filter %q, subfioter %v", f.Id, i)
		}
		err = filterInstaller.InstallDependencies(f)
		if err != nil {
			return WrapErrorf(
				err,
				"could not install dependencies for filter %q, subfilter %v",
				f.Id, i)
		}
	}
	return nil
}

func (f *RemoteFilterDefinition) Check() error {
	return nil
}

func (f *RemoteFilter) Check() error {
	return f.Definition.Check()
}

func (f *RemoteFilter) CopyArguments(parent *RemoteFilter) {
	// We don't support nested remote filters anymore so this function is
	// never called.
	f.Arguments = parent.Arguments
	f.Settings = parent.Settings
	f.Definition.VenvSlot = parent.Definition.VenvSlot
}

func (f *RemoteFilter) GetFriendlyName() string {
	if f.Name != "" {
		return f.Name
	} else if f.Id != "" {
		return f.Id
	}
	_, end := path.Split(f.Definition.Url) // Return the last part of the URL
	return end
}

// CopyFilterData copies the filter's data to the data folder.
func (f *RemoteFilterDefinition) CopyFilterData(dataPath string) {
	// Move filters 'data' folder contents into 'data'
	// If the localDataPath already exists, we must not overwrite
	// Additionally, if the remote data path doesn't exist, we don't need
	// to do anything
	remoteDataPath := path.Join(f.GetDownloadPath(), "data")
	localDataPath := path.Join(dataPath, f.Id)
	if _, err := os.Stat(localDataPath); err == nil {
		Logger.Warnf("Filter %s already has data in the 'data' folder. \n"+
			"You may manually delete this data and reinstall if you "+
			"would like these configuration files to be updated.",
			f.Id)
	} else if _, err := os.Stat(remoteDataPath); err == nil {
		// Ensure folder exists
		err = os.MkdirAll(localDataPath, 0666)
		if err != nil {
			Logger.Error("Could not create filter data folder", err)
		}

		// Copy 'data' to dataPath
		if dataPath != "" {
			err = copy.Copy(
				remoteDataPath, localDataPath,
				copy.Options{PreserveTimes: false, Sync: false})
			if err != nil {
				Logger.Error("Could not initialize filter data", err)
			}
		} else {
			Logger.Warnf(
				"Filter %s has installation data, but the "+
					"dataPath is not set. Skipping.", f.Id)
		}
	}
}

// GetDownloadPath returns the path location where the filter can be found.
func (f *RemoteFilter) GetDownloadPath() string {
	return filepath.Join(".regolith/cache/filters", f.Id)
}

// IsCached checks whether the filter of given URL is already saved
// in cache.
func (f *RemoteFilter) IsCached() bool {
	_, err := os.Stat(f.GetDownloadPath())
	return err == nil
}

// FilterDefinitionFromTheInternet downloads a filter from the internet and
// returns its data.
func FilterDefinitionFromTheInternet(
	url, name, version string,
) (*RemoteFilterDefinition, error) {
	version, err := GetRemoteFilterDownloadRef(url, name, version, false)
	if err == nil {
		return &RemoteFilterDefinition{
			FilterDefinition: FilterDefinition{Id: name},
			Version:          version,
			Url:              url,
		}, nil
	}
	return nil, WrapErrorf(
		nil, "no valid version found for filter %q", name)
}

// Download
func (i *RemoteFilterDefinition) Download(isForced bool) error {
	if i.IsInstalled() {
		if !isForced {
			Logger.Warnf("Filter %q already installed, skipping. Run "+
				"with '-f' to force.", i.Id)
			return nil
		} else {
			// TODO should we print version information here?
			// like "version 1.4.2 uninstalled, version 1.4.3 installed"
			Logger.Warnf("Filter %q already installed, but force mode is enabled.\n"+
				"Filter will be installed, erasing prior contents.", i.Id)
			i.Uninstall()
		}
	}

	Logger.Infof("Downloading filter %s...", i.Id)

	// Download the filter using Git Getter
	// TODO:
	// Can we somehow detect whether this is a failure from git being not
	// installed, or a failure from
	// the repo/folder not existing?
	url, err := i.GetDownloadUrl()
	if err != nil {
		return WrapError(err, "unable to get download URL")
	}
	downloadPath := i.GetDownloadPath()
	err = getter.Get(downloadPath, url)
	if err != nil {
		return WrapErrorf(
			err,
			"Could not download filter from %s.\n"+
				"	Is git installed?\n"+
				"	Does that filter exist?", url)
	}

	// Remove 'test' folder, which we never want to use (saves space on disk)
	testFolder := path.Join(downloadPath, "test")
	if _, err := os.Stat(testFolder); err == nil {
		os.RemoveAll(testFolder)
	}

	Logger.Infof("Filter %s downloaded successfully.", i.Id)
	return nil
}

// GetDownloadUrl creates a download URL, based on the filter definition
func (i *RemoteFilterDefinition) GetDownloadUrl() (string, error) {
	repoVersion, err := GetRemoteFilterDownloadRef(
		i.Url, i.Id, i.Version, true)
	if err != nil {
		return "", WrapErrorf(
			err, "unable to get download URL for filter %q", i.Id)
	}
	return fmt.Sprintf("%s//%s?ref=%s", i.Url, i.Id, repoVersion), nil
}

// GetDownloadPath returns the path location where the filter can be found.
func (i *RemoteFilterDefinition) GetDownloadPath() string {
	return filepath.Join(".regolith/cache/filters", i.Id)
}

func (i *RemoteFilterDefinition) Uninstall() {
	err := os.RemoveAll(i.GetDownloadPath())
	if err != nil {
		Logger.Error(
			WrapErrorf(err, "Could not remove installed filter %s.", i.Id))
	}
}

// IsInstalled eturns whether the filter is currently installed or not.
func (i *RemoteFilterDefinition) IsInstalled() bool {
	if _, err := os.Stat(i.GetDownloadPath()); err == nil {
		return true
	}
	return false
}
