# Catatan wawancara

## Penjelasan singkat

WebhookLab adalah laboratorium integrasi lokal untuk melihat pengiriman webhook ketika penerima sehat, gagal, atau terlambat merespons. API menyimpan event dan antrean awal dalam satu transaksi. Worker terpisah mengirim request bertanda tangan HMAC, mencatat setiap attempt, dan mencoba ulang secara terbatas. Receiver sintetis menyimpan receipt serta mencegah efek kedua untuk event yang sama.

Bagian yang perlu dijelaskan adalah perbedaan antara hasil yang diketahui sender dan efek yang mungkin sudah terjadi pada receiver. Request timeout tidak membuktikan bahwa receiver belum memproses event. Aplikasi menampilkan dua bukti itu secara terpisah.

Stack-nya adalah Next.js static export, Go untuk API/worker/receiver, PostgreSQL 17, Docker, dan Nginx. Next.js benar-benar membangun frontend, tetapi aplikasi ini tidak menjalankan SSR atau server actions. Tidak ada integrasi pembayaran eksternal atau pemrosesan order sungguhan.

## Demo yang dapat diulang

1. Login dengan akun operator lokal, lalu kirim event sintetis ke receiver Healthy. Buka attempt dan receipt yang terbentuk.
2. Ubah receiver menjadi Fail. Kirim event baru dan amati tiga attempt, lalu dead letter. Tunjukkan bahwa receiver tidak mencatat efek untuk request yang sengaja mengembalikan 503.
3. Ubah receiver menjadi Healthy dan redrive event tadi dengan note yang spesifik. History attempt sebelumnya harus tetap ada.
4. Ubah receiver menjadi Timeout dan kirim event baru. Receiver mencatat efek sebelum menunda respons melewati timeout worker. Tunjukkan attempt timeout dan receipt dengan satu efek.
5. Setelah event itu dead letter, ubah receiver menjadi Healthy dan redrive. Receipt duplicate membuktikan bahwa receiver mengenali event yang sama dan tidak menambah efek kedua.

Mode receiver berlaku untuk seluruh workspace. Gunakan event berbeda untuk skenario berbeda dan jangan mengubah mode saat mengklaim bahwa sebuah attempt tertentu telah memakai mode sebelumnya. Bukti utamanya adalah attempt serta receipt tersimpan, bukan posisi kontrol saat ini.

## Apa yang dimaksud durable outbox?

Event dan delivery berstatus QUEUED disimpan dalam satu transaksi PostgreSQL. Jika transaksi gagal, keduanya batal. Jika commit berhasil tetapi respons ke browser hilang, event tetap ada dan worker dapat mengambilnya.

Create memakai idempotency key yang terikat pada aktor serta fingerprint destination, event type, dan payload yang telah di-compact. Browser dapat mengulang request yang belum pasti dengan key dan input yang sama. Server mengembalikan event yang sudah ada, bukan membuat event kedua. Key yang sama dengan konten berbeda ditolak.

Ini menyelesaikan batas antara penyimpanan event dan antrean awal pada aplikasi ini. Outbox tidak dengan sendirinya menjamin bahwa HTTP ke receiver hanya terjadi sekali.

## Mengapa database transaction tidak meliputi HTTP?

Receiver dapat lambat atau tidak menjawab. Menahan transaction dan row lock selama network I/O memperpanjang penggunaan koneksi database dan menyulitkan worker lain.

Worker memakai transaksi pendek untuk claim: pilih pekerjaan yang jatuh tempo, simpan STARTED attempt dan lease, lalu commit. Setelah request selesai, transaksi baru mencatat hasil hanya jika worker masih memiliki claim tersebut. Jika proses mati di antaranya, recovery dapat menemukan lease yang kedaluwarsa.

Pada attempt ABANDONED, `finishedAt` menunjukkan waktu recovery lease dan `durationMs` menghitung selang dari awal attempt sampai recovery. Angka itu dapat melewati timeout HTTP dua detik karena mencakup waktu menunggu recovery, bukan durasi respons HTTP yang diterima.

## Apa perbedaan lock, lease, dan fencing?

Row lock mengoordinasikan pemilihan pekerjaan saat transaksi claim berjalan. `SKIP LOCKED` memungkinkan worker lain memilih row yang berbeda. Lease mempertahankan identitas pekerjaan yang sedang berjalan setelah transaksi itu selesai.

Fencing memeriksa token lease dan version pada penyimpanan hasil. Worker lama yang terlambat kembali tidak boleh menimpa state milik claim lebih baru. Perlindungan ini berlaku pada state sender; fencing tidak dapat membatalkan request atau efek receiver yang sudah terjadi.

## Apakah ini exactly-once?

Semantik attempt-nya at least once: event yang sama dapat dikirim lebih dari sekali. Response dapat hilang setelah efek berhasil, sehingga sender dapat mengirim ulang. Anggaran retry terbatas dan dapat berakhir di DEAD_LETTER setelah seluruh cycle habis; ini bukan jaminan event akhirnya berhasil terkirim.

Receiver lokal menyediakan deduplikasi per destination dan event ID dalam transaksi dengan unique constraint. Body hash harus tetap sama. Event pertama menghasilkan satu efek sintetis; request identik berikutnya hanya menambah receipt count. Jaminan efek lokal itu bergantung pada implementasi receiver, bukan sesuatu yang dapat dipaksakan sender pada setiap endpoint eksternal.

## Mengapa Timeout mode tetap bisa mempunyai receipt?

Dalam mode itu, receiver menyimpan receipt/effect terlebih dahulu, lalu menunda respons selama 3,5 detik. Worker membatasi request menjadi 2 detik. Sender mengetahui bahwa ia tidak memperoleh respons tepat waktu, tetapi tidak dapat menyimpulkan bahwa proses remote belum terjadi.

Dashboard dapat membaca receipt karena receiver demo memakai database lokal yang dapat diamati bersama. Dalam integrasi antarorganisasi, akses seperti itu biasanya tidak tersedia. Receipt adalah bukti tambahan di lab, bukan pengetahuan otomatis dari sebuah timeout.

## Mengapa retry tiga kali dan redrive dibatasi?

Satu cycle mempunyai tiga attempt, dengan jeda retry 2 detik dan 5 detik. Ini kebijakan demo yang tetap, bukan hasil optimasi untuk SLA produksi. Setelah cycle habis, state menjadi DEAD_LETTER agar operator dapat memeriksa penyebabnya.

Redrive hanya tersedia dari DEAD_LETTER, memerlukan version saat ini dan note, serta dibatasi dua kali. Total maksimal sembilan attempt menjaga history tetap terbatas. Redrive tidak menghapus bukti lama dan tidak menciptakan identitas event baru.

## Apakah pause menghentikan request yang sudah dikirim?

Pause menghentikan claim baru. Kontrol pause dan claim bertemu pada lock row kontrol, sehingga urutannya jelas. Request yang claim-nya telah commit masih boleh selesai. Recovery lease dan heartbeat tetap berjalan ketika pause.

UI menampilkan waktu worker terakhir terlihat dalam 15 detik. Itu observasi heartbeat, bukan janji bahwa worker sedang sehat atau seluruh request akan selesai.

## Bagaimana HMAC digunakan?

Worker dan receiver berbagi master secret dari konfigurasi server. Kunci tiap destination diturunkan dari master dengan HMAC-SHA256, lalu dipakai untuk menandatangani timestamp, event ID, dan byte envelope yang persis sama. Receiver memeriksa signature secara constant time, toleransi timestamp, ukuran, dan kecocokan ID sebelum menyimpan receipt.

HMAC membuktikan request sesuai dengan secret dan konten yang ditandatangani. Deduplikasi serta body hash menangani request yang berulang. Browser tidak melihat signing secret atau signature aktual, dan pengguna tidak dapat memasukkan URL tujuan arbitrer.

## Mengapa payload tidak dibaca menjadi float64?

Payload adalah objek JSON generik. Angka besar atau representasi angka tertentu dapat berubah jika diparse ke floating point lalu diserialisasi ulang. Backend memvalidasi serta meng-compact teks JSON, menyimpan envelope immutable, dan menandatangani byte itu. Frontend menampilkan payload sebagai teks.

Tujuannya menjaga identitas konten yang dikirim. Aplikasi tidak menghitung saldo atau memberi makna bisnis pada field payload.

## Mengapa memakai PostgreSQL tanpa broker tambahan?

Lab dibatasi 500 event dan dirancang untuk memperlihatkan transaksi, claim, lease, retry, serta recovery secara langsung. PostgreSQL sudah menyediakan transaksi dan row lock yang dibutuhkan untuk lingkup ini.

Jika kebutuhan berubah menjadi volume lebih besar atau banyak consumer, saya perlu mengukur beban dan mengevaluasi pemisahan broker, partisi, fairness, backpressure, dan operasi sistemnya. Menambah broker tidak menghilangkan kemungkinan duplicate atau kebutuhan receiver yang idempotent.

## Apa yang menjadi bukti proyek ini bekerja?

Baca [VERIFICATION.md](VERIFICATION.md) untuk perintah dan hasil yang benar-benar dijalankan. Nama test atau keberadaan fungsi tidak membuktikan test tersebut lulus. Pisahkan unit test, test dengan PostgreSQL, workflow browser, dan observasi proses worker yang benar-benar dihentikan.

Skenario yang patut diperlihatkan mencakup create bersamaan dengan key sama, claim paralel tanpa pekerjaan ganda yang aktif, completion worker lama yang ditolak fencing, recovery lease, tiga attempt per cycle, batas redrive, HMAC invalid yang ditolak, duplicate receipt dengan satu efek, serta uncertain create response yang diulang tanpa event kedua.

Jangan menambahkan klaim throughput, uptime, jumlah pengguna, atau kesiapan produksi tanpa bukti yang sesuai. Untuk ringkasan CV setelah verifikasi, jelaskan fitur dan skenario yang dibuktikan, bukan hanya daftar framework.

## Batasan yang perlu disampaikan

Workspace lokal memiliki satu operator dan dua destination tetap. Pengaturan mode receiver serta antrean bersifat bersama. Tidak ada pengiriman ke URL publik arbitrer, per-tenant authorization, manajemen secret untuk pihak eksternal, pembayaran nyata, atau kebijakan retensi otomatis.

Pekerjaan lanjutan perlu mengikuti kebutuhan: observability dan backpressure untuk operasi, isolasi tenant untuk layanan multi-organisasi, atau protokol receiver yang lebih kaya untuk integrasi nyata. Pilihan itu memerlukan desain dan pengujian tambahan.
