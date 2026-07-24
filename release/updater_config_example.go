package releaseassets

import _ "embed"

// updaterConfigExample is compiled into autostream-updater so first-time
// configuration does not depend on a particular release-directory layout.
//
//go:embed autostream-updater.json.example
var updaterConfigExample []byte

// UpdaterConfigExample returns an independent copy of the bundled initial
// updater configuration.
func UpdaterConfigExample() []byte {
	return append([]byte(nil), updaterConfigExample...)
}
