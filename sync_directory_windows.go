package main

func syncMaterialDirectory(path string) error {
	// Windows cannot FlushFileBuffers on a read-only directory handle. File
	// contents are already synced before rename; validate the parent instead.
	return sessionCheckDir(path)
}
