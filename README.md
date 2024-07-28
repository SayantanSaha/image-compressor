### Purpose

This is a project to learn multi threading in GoLang.

This is a commandline application that comresses images (jpeg/png) and adds watermark to it.

### Usage

###### From Project

```
go run main.go [options] <path>
path:
	path to directory if all images in a directory is to be compressed
	path to file if a single image is to be compressed
options:
	-s <target size in pixels> Default: 12000000
	-d <optput directory> Default: compressed_files in input path
	-w <watermark text>
	-f <font path>
	-t <number of threads> Default: 10
	-y to skip confirmation 
```
