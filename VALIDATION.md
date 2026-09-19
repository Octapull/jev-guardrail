# Doğrulama — 19 Eylül 2026

macOS arm64, Go 1.26.3 üzerinde yerel doğrulama.

## Gerçek TypeSafe API

Kullanıcının sağladığı anahtarla toplam **4 küçük çağrı** yapıldı.
Hiçbir upstream sohbet modeli çağrılmadı, kredi yüklenmedi.

| Örnek | Kullanılan tip | Sonuç | Giriş tokenı | Gecikme |
| --- | --- | --- | ---: | ---: |
| Go HTTP sunucusu hakkında normal soru | Noul | allow | 347 | 887 ms |
| Sistem talimatlarını açığa çıkarma girişimi | Noul | block, p=0.99 | 346 | 800 ms |
| Kibar teşekkür mesajı | Score | allow | 352 | 760 ms |
| Yetkisiz özel veri gönderme araç önerisi | 3 Noul + Choice | block, exfiltration p=0.96 | 537 | 741 ms |

Toplam **1.582 giriş tokenı**. Resmi 0,042 USD / milyon giriş tokenı fiyatıyla
tahmini maliyet **0,000066444 USD**. Bu bir bakiye veya fatura sorgusu değildir.
Yerel ihtiyatlı sayaçta **0,04 USD rezervasyon**, **4,46 USD / 446 deneme** kaldı.
Son üç çağrının ayrıntılı yerel JSON kaydı `.jevguard/live-smoke.json` içindedir;
çalıştırma verileri Git dışında tutulur.

Bu örnekler API entegrasyonunun çalıştığını gösterir. Dört örnek üzerinden
genel güvenlik doğruluğu, kalibrasyon veya gecikme yüzdeliği iddiası kurulamaz.
Bu makinedeki yaklaşık 0,74–0,89 saniyelik ilk çağrılar 100 ms vaadini desteklemez.

## Otomatik kontroller

`go test -race ./...`, `go vet ./...` ve CLI derlemesi doğrulandı.
Testler gerçek API'yi çağırmaz; HTTP testleri yalnızca localhost kullanır.

- 20 eşzamanlı sayaç kullanıcısı, yeniden açılan dosya, 450 denemelik sınır,
  bozuk muhasebe dosyası ve rezervasyonların iade edilmemesi.
- HTTP sözleşmesi, kimlik doğrulama başlığı, retry başına rezervasyon,
  iptal/deadline, büyük gövde, redirect reddi ve sağlayıcı hata gövdesinin gizlenmesi.
- Noul eşik sınırları, Choice/Score kararları, belirsizlik, hatada açık/kapalı
  politika, yanlış yanıtlar, toplu soru gönderimi, eşzamanlı önbellek ve LRU tahliyesi.
- Middleware gövde/context koruması, input engelinde upstream'e çağrı yapılmaması,
  output engelinde cevabın sızmaması, streaming ve görsel girdinin reddedilmesi.
- Ücretsiz CLI komutları, init dosyasını üzerine yazmama, dotenv'in komut
  çalıştırmadan okunması, benchmark hata/inceleme ayrımı ve içeriksiz log/metrikler.
