# پروژه Dynamic Service Discovery با Consul و Nginx و Registrator

## معرفی
این پروژه نمونه‌ای است برای راه‌اندازی سرویس‌ها با Docker به شکلی که سرویس‌ها به صورت اتوماتیک در Consul ثبت شوند و Nginx بتواند به صورت داینامیک به آنها روت کند.

## ساختار پروژه
- `docker-compose.yml`: تعریف کانتینرهای Consul، Registrator، Nginx و سرویس `myapp`
- `nginx/nginx.conf`: کانفیگ پایه Nginx
- `nginx/templates/upstream.ctmpl`: قالب داینامیک Nginx برای سرویس‌های ثبت شده در Consul

## نحوه کار
- سرویس‌ها بدون expose کردن پورت به هاست اجرا می‌شوند.
- Registrator با پارامتر `-internal` به صورت اتوماتیک سرویس‌های Docker را در Consul ثبت می‌کند.
- Nginx کانفیگ خود را از فایل قالب دریافت می‌کند و به سرویس‌های موجود در Consul روت می‌کند.

## راه‌اندازی
1. این پروژه را کلون یا دانلود کنید.
2. سرویس `myapp` را جایگزین `yourappimage:latest` در `docker-compose.yml` کنید.
3. دستور زیر را برای راه‌اندازی اجرا کنید:

```bash
docker-compose up -d
