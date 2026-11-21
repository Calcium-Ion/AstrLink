package plugininstall

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// localFolder is the folder a link names when it is an absolute path on
// the computer Core runs on.
func localFolder(link string) (string, bool) {
	link = strings.TrimSpace(link)
	if link == "" || !filepath.IsAbs(link) {
		return "", false
	}
	return filepath.Clean(link), true
}

// buildLocal builds the plugin in folder as it holds it now, the way a
// GitHub commit is built: the folder is only read, and its dependencies
// come by its lock file.
func (installer *Installer) buildLocal(ctx context.Context, folder, work string, existing func(string) *Existing) (contract.PluginInstallReview, contract.PluginInstallRecord, error) {
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, fail(contract.PluginInstallFolderNotFound, folder)
	}
	source := contract.PluginInstallSource{Kind: contract.PluginSourceLocal, Repository: filepath.Base(folder), URL: folder}
	return installer.buildSource(ctx, origin{source: source}, folder, work, existing)
}
