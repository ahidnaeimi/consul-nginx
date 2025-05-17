# Consul + Nginx + Consul Template (Dynamic Service Discovery)

این پروژه یک نمونه ساده از استفاده ترکیبی **Nginx**, **Consul** و **Consul Template** است برای حل مشکلات رایجی که در محیط‌های داکری با آن مواجه می‌شویم:

---

## 🧩 مشکل اصلی

وقتی از Nginx به عنوان Reverse Proxy استفاده می‌کنیم، معمولاً تنظیمات آن به صورت استاتیک است. این باعث می‌شود:

1. اگر سرویسی که در تنظیمات Nginx آمده، در دسترس نباشد، Nginx به مشکل بخورد یا بالا نیاید.
2. هر بار که آدرس یا تعداد سرویس‌ها تغییر می‌کند، نیاز به ویرایش دستی تنظیمات و reload کردن Nginx داریم.
3. هماهنگی بین CI/CD و load balancer به سختی انجام می‌شود.

---

## ✅ راه‌حل: Service Discovery با Consul + قالب‌سازی با Consul Template

### اجزای اصلی:

- **Consul**: کشف و نگهداری اطلاعات سرویس‌ها به صورت داینامیک.
- **Consul Template**: تولید داینامیک فایل کانفیگ Nginx از داده‌های Consul.
- **Nginx**: عمل به‌عنوان reverse proxy به سمت سرویس‌های ثبت‌شده در Consul.
- **App (مثال)**: یک سرویس ساده که خودش را به Consul رجیستر می‌کند.

---

## 📁 ساختار پروژه

```
.
├── docker-compose.yml
└── nginx/
    ├── nginx.conf              # کانفیگ اصلی Nginx
    ├── nginx.ctmpl             # تمپلیت کانفیگ که توسط consul-template پر می‌شود
    └── templates/
        └── upstream.conf       # فایل تولیدشده برای upstream
```

---

## ▶️ اجرای پروژه

```bash
docker compose up --build
```

- اپلیکیشن روی پورت 9000 اجرا می‌شود.
- Nginx روی پورت 8080 به عنوان ورودی عمل می‌کند.
- خروجی `http://localhost:8080` باید متن `hello from app` را نشان دهد.

---

## ⚙️ تنظیمات کلیدی

### nginx.ctmpl

```nginx
upstream backend {
  {{ range service "myapp" }}
  server {{ .Address }}:{{ .Port }};
  {{ end }}
}
```

این تمپلیت توسط `consul-template` پر شده و upstream داینامیکی می‌سازد.

---

## 🧠 نتیجه

این روش:
- Nginx را در برابر تغییرات مقاوم می‌کند.
- Deploy جدید را بدون نیاز به reload دستی پشتیبانی می‌کند.
- پایه‌ای برای ساختن زیرساخت‌های مقیاس‌پذیر microservice فراهم می‌کند.

