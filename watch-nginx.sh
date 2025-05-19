#!/bin/bash

WATCH_DIR="./shared-nginx-templates"

inotifywait -m -e create,modify,delete,move "$WATCH_DIR" | while read path action file; do
  echo "File $file was $action, running your command..."
  # دستور مورد نظر:
  docker kill -s HUP nginx
done

# sudo apt-get install inotify-tools
