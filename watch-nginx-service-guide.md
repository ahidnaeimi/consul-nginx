
# راهنمای کامل ساخت پایشگر دایرکتوری و سرویس systemd برای ریستارت nginx در Docker

این مستند گام‌به‌گام نحوه ساخت اسکریپت bash برای پایش تغییرات دایرکتوری با inotifywait و تبدیل آن به سرویس systemd که هنگام تغییرات nginx را ریستارت می‌کند را توضیح می‌دهد.

---

## مرحله 1: نصب پیش‌نیازها

ابتدا ابزار inotifywait را نصب کنید:

sudo apt-get update  
sudo apt-get install inotify-tools

همچنین مطمئن شوید دسترسی لازم به اجرای دستورات Docker را دارید (معمولاً کاربر باید عضو گروه docker باشد یا اسکریپت با root اجرا شود).

---

## مرحله 2: ساخت اسکریپت پایشگر (Watcher Script)

1. یک فایل اسکریپت ایجاد کنید، مثلاً:

sudo nano /usr/local/bin/watch-nginx-templates.sh

2. محتوای زیر را در آن قرار دهید:

#!/bin/bash

WATCH_DIR="/path/to/shared-nginx-templates"

inotifywait -m -e create,modify,delete,move "$WATCH_DIR" | while read path action file; do  
    echo "File $file was $action, running your command..."  
    docker kill -s HUP nginx  
done

3. مسیر /path/to/shared-nginx-templates را به مسیر واقعی دایرکتوری که می‌خواهید پایش کنید تغییر دهید.

4. اجازه اجرای اسکریپت را بدهید:

sudo chmod +x /usr/local/bin/watch-nginx-templates.sh

---

## مرحله 3: ساخت فایل سرویس systemd

1. فایل سرویس جدید بسازید:

sudo nano /etc/systemd/system/watch-nginx.service

2. محتوای زیر را در آن قرار دهید:

[Unit]  
Description=Watch nginx template changes and reload container  
After=network.target docker.service  
Requires=docker.service

[Service]  
ExecStart=/usr/local/bin/watch-nginx-templates.sh  
Restart=always  
User=root  
WorkingDirectory=/usr/local/bin

[Install]  
WantedBy=multi-user.target

---

## مرحله 4: فعال‌سازی و اجرای سرویس

برای معرفی سرویس به systemd و اجرای آن دستورات زیر را اجرا کنید:

sudo systemctl daemon-reexec  
sudo systemctl daemon-reload  
sudo systemctl enable --now watch-nginx.service

---

## مرحله 5: بررسی وضعیت سرویس

برای اطمینان از اجرای سرویس:

sudo systemctl status watch-nginx.service

---

## مرحله 6: مشاهده لاگ‌های سرویس

برای دیدن خروجی و خطاهای سرویس:

journalctl -u watch-nginx.service -f

---

## نکات مهم

- اگر نمی‌خواهید سرویس با کاربر root اجرا شود، مطمئن شوید کاربر دیگر عضو گروه docker است.  
- مسیرهای موجود در اسکریپت و فایل سرویس را متناسب با محیط خود اصلاح کنید.  
- با گزینه Restart=always در فایل سرویس، در صورت توقف ناخواسته، سرویس مجدد اجرا می‌شود.

---

## خلاصه دستورات

sudo apt-get install inotify-tools  
sudo nano /usr/local/bin/watch-nginx-templates.sh  
sudo chmod +x /usr/local/bin/watch-nginx-templates.sh  
sudo nano /etc/systemd/system/watch-nginx.service  
sudo systemctl daemon-reexec  
sudo systemctl daemon-reload  
sudo systemctl enable --now watch-nginx.service  
sudo systemctl status watch-nginx.service  
journalctl -u watch-nginx.service -f

---

اگر خواستی برات همین رو فایل md کنم بگو تا بفرستم.
