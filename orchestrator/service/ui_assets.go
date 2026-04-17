package service

import (
	"embed"
	"io/fs"
)

//go:embed web/*
var dashboardAssets embed.FS

var dashboardAssetFS = mustSubFS(dashboardAssets, "web")
var dashboardStaticFS = mustSubFS(dashboardAssetFS, "static")

func mustSubFS(root fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(root, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
