package sprout

import (
	"embed"

	"github.com/wxy365/basal/i18n"
	"github.com/wxy365/basal/log"
)

//go:embed i18n
var fs embed.FS

func init() {
	err := i18n.AddMessagesFromEmbedFS(fs)
	if err != nil {
		log.WarnErrF("Failed to load i18n resources", err)
	}
}
