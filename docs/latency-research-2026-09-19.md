# Guardrail gecikmesi: 19 Eylül 2026 araştırması

Jev'in genel amaçlı bir LLM'yi hakem olarak kullanmaya karşı gecikme avantajı
olabileceğine dair ölçüm var. Jev'in bütün özel güvenlik sınıflandırıcılarından
daha hızlı olduğunu veya eşit koruma kalitesi verdiğini gösteren bir sonuç bulamadım.
Bu araştırmada yeni ücretli API çağrısı yapılmadı.

## Birincil kaynaklar ve neyi gösterdikleri

1. [WotAI — Jev vs 15 model, 18 Eylül 2026](https://wotai.co/blog/typesafe-jev-vs-claude-haiku-tested).
   Yazarın kendi deneyi: 150 metin parçasının üslubunu ayırt etme görevinde
   yayımlanmış p50 Jev 455 ms, Haiku 4.5 631 ms, Sonnet 5 1674 ms.
   Jev-Haiku farkı 176 ms (%27,9 daha az süre; 1,39× oran);
   Jev-Sonnet farkı 1219 ms (%72,8 daha az süre; 3,68× oran).
   Diğer iki sınıflandırma görevinde Haiku'ya karşı oran 1,49× ve 1,62×.
   Bu, sağlayıcı dışından bir deney raporu; bizim yeniden ürettiğimiz veya
   bağımsız denetimden geçmiş bir guardrail benchmark'ı değil. Üslup sınıflandırması
   saldırı yakalama doğruluğuna genellenemez. Raporda fiyat bilgisinin bulunmadığı
   söyleniyor; o iddiayı benimsemiyoruz, TypeSafe'in lansman yazısında fiyat mevcut.

2. [TypeSafe'in lansman raporu, 15 Eylül 2026](https://typesafe.ai/blog/introducing-system-one-models-and-jev).
   Sağlayıcı 70–500 ms ve büyük hız oranları bildiriyor. Karşılaştırma System One
   biçimli iş akışlarına ait; genel bir prompt-injection karşılaştırması değil.
   Kendi ölçümlerinin çoğunun servisin bulunduğu ABD Batı Kıyısı'ndan yapıldığını
   da belirtiyor. Ağ konumu ve iş yükü değiştiğinde bu sayılar garanti değil.

3. [Lakera Guard Fall ’25, 20 Ekim 2025](https://www.lakera.ai/blog/lakera-guard-fall-25-adaptive-at-scale).
   Sağlayıcı kısa metinler için 40 ms altında, çok uzun isteklerde yaklaşık
   200 ms altında gecikme bildiriyor. Bu sayılar aynı istemci ve veriyle ölçülmüş
   Jev karşılaştırması değil. Yine de “bütün guardrail'ler saniyeler sürer”
   önermesini desteklemiyor; özel sınıflandırıcılar farklı bir kategoridir.

4. [NVIDIA NeMo runtime security FAQ](https://docs.nvidia.com/nemo/guardrails/resources/runtime-security-faq)
   ve [speculative generation](https://docs.nvidia.com/nemo/guardrails/configure-guardrails/yaml-schema/guardrails-configuration).
   Bağımsız kontroller paralel çalıştırılabilir; paralellik Jev'e özgü değildir.
   Bazı yapılandırmalarda giriş kontrolü ve cevap üretimi de örtüştürülebilir.
   Gecikme dağılımı model, donanım, ağ, içerik uzunluğu, eşzamanlılık ve hangi
   kontrollerin etkin olduğuna bağlıdır.

5. [Mozilla.ai'nin kendi guardrail değerlendirmesi](https://blog.mozilla.ai/can-open-source-guardrails-really-protect-ai-agents/).
   Saldırı verisinin türü değişince precision/recall sonuçları belirgin değişiyor.
   Hız ölçümüyle güvenlik başarısını aynı şey saymamak gerekir; Jev bu eski çalışmada yok.

## Bu projede elimizdeki gerçek ölçüm

[Önceki dört canlı çağrı](../VALIDATION.md):
887,270 / 800,047 / 759,611 / 741,202 ms. Aritmetik ortalama **797,0325 ms**.
Farklı içerik ve politikalar, ayrı CLI süreçleri; küçük entegrasyon kontrolü.
Bu dağılımdan üretim p95/p99, sunucu içi çıkarım süresi veya güvenilir throughput
tahmini çıkarılamaz. TLS/bağlantı kurulumu ve ağ dahil uçtan uca sürelerdir.
Uzun yaşayan proxy bağlantıları farklı performans gösterebilir.

Mevcut kod sırası:

Giriş kontrolü → upstream'in tüm cevabı → çıkış kontrolü → kullanıcıya gönderim.

Streaming kapalı. İki kontrol de yaklaşık 797 ms sürerse, toplam guardrail ek
süresi yaklaşık **1,594 saniye** olur. Bu bir hesap; iki aşamalı canlı proxy
ölçümü değildir. Uzun çıktının kontrolü daha uzun sürebilir.

## Animasyondaki senaryo

Güvenli istek, önbellek yok, ana modelin tam cevap süresi varsayılan 3 saniye:

| Yapılandırma | Tam cevaba kadar süre |
| --- | ---: |
| Kontrolsüz referans | 3,000 sn |
| Jev giriş + çıkış, kontrol başına 0,797 sn tahmini | 4,594 sn |
| LLM hakem giriş + çıkış, kontrol başına 1,500 sn varsayımı | 6,000 sn |

Bu varsayımsal LLM hakeme göre Jev yaklaşık 1,406 saniye / %23,4 kazandırır.
Kontrolsüz sisteme göre ise ek süre getirir. Ana modeli hızlandırmaz.
Buradaki 1,5 saniye hiçbir sağlayıcının doğrulanmış guardrail ölçümü olarak sunulmaz.
Animasyondaki ilk grafik ayrı olarak WotAI'nin yayımladığı tek-karar p50'lerini gösterir.

## Proje için sonuç

Jev'i “her guardrail'den 100× hızlı” diye konumlandırmak için veri yok.
“Özel kuralları tek çağrıda, yapılandırılmış kararlar halinde değerlendiren ve
LLM hakeme alternatif olabilen ucuz Go geçidi” mevcut kanıtla daha uyumludur.
Genel LLM'ler de kontrolleri tek istekte birleştirebilir; kıyas buna izin vermeli.

Adil bir sonraki deney: aynı istemci konumu ve aynı saldırı/normal örnekler,
eşdeğer kurallar, bağlantı ısınması ayrılmış, sabit eşzamanlılık; Jev ile en az bir
özel sınıflandırıcı ve bir LLM hakem için p50/p95, hata oranı, false positive,
false negative ve maliyet birlikte ölçülmeli. Bu araştırma o deneyi yapmış değildir.
