package main

import (
	"log"

	"labnana2api/internal/config"
	"labnana2api/internal/gallery"
	"labnana2api/internal/server"
)

func main() {
	cfgStore := config.NewStore("config.json")
	if err := cfgStore.Load(); err != nil {
		log.Fatalf("load config: %v", err)
	}

	galleryStore, err := gallery.New(".runtime/gallery.json", ".runtime/media")
	if err != nil {
		log.Fatalf("init gallery: %v", err)
	}

	srv, err := server.New(cfgStore, galleryStore)
	if err != nil {
		log.Fatalf("init server: %v", err)
	}

	if err := srv.Run(); err != nil {
		log.Fatal(err)
	}
}
