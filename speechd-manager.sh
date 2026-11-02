#!/bin/bash

case "$1" in
    start)
        echo "启动 speech-dispatcher..."
        speech-dispatcher -d
        ;;
    stop)
        echo "停止 speech-dispatcher..."
        pkill -f speech-dispatcher
        ;;
    restart)
        echo "重启 speech-dispatcher..."
        pkill -f speech-dispatcher
        sleep 2
        speech-dispatcher -d
        ;;
    status)
        if pgrep -f speech-dispatcher > /dev/null; then
            echo "speech-dispatcher 正在运行"
            ps aux | grep speech-dispatcher | grep -v grep
        else
            echo "speech-dispatcher 未运行"
        fi
        ;;
    *)
        echo "用法: $0 {start|stop|restart|status}"
        exit 1
        ;;
esac