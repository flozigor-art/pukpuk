# Развёртывание LangPlant CRM

Эта инструкция написана для агента или человека, у которого есть доступ к VPS и к компу Саши.
Компонентов два:

| Где | Что | Ресурсы |
|---|---|---|
| **VPS** | `crm-server`: сайт, API, база SQLite, буфер загрузок | ~20–60 МБ RAM, диск: буфер + кэш (настраивается) |
| **Комп Саши** | `crm-node`: основной архив файлов, ffmpeg, бэкапы базы | диск под архив, CPU для превью |

Нода **сама подключается** к VPS по `wss://<домен>/api/node/ws`. На компе не нужны белый IP, проброс портов и VPN. Весь трафик идёт по HTTPS на 443-м порту.

---

## 0. Что понадобится

- Домен или поддомен для CRM, например `crm.example.com`, с A-записью на IP VPS.
- На VPS: Docker + Compose plugin (или просто бинарник и systemd, см. вариант Б), открытые порты 80 и 443.
- На компе: Docker Desktop (Windows/macOS) или Docker Engine (Linux), папка на большом диске под архив.
- Общий секрет для связи сервера и ноды:
  ```bash
  openssl rand -hex 32
  ```
  Это значение записывается в `CRM_NODE_TOKEN` на VPS и в `NODE_TOKEN` на компе.

---

## 1. VPS

### Вариант А: Docker Compose (рекомендуется)

```bash
git clone <repo> && cd <repo>/langplant-crm/deploy/server
cp .env.example .env
# в .env заполнить CRM_PUBLIC_URL, CRM_DOMAIN, CRM_NODE_TOKEN; лимиты диска — по месту на VPS
```

**Если на VPS ещё нет веб-сервера**, поднимите сервер вместе с Caddy. Caddy сам получит сертификат Let's Encrypt:
```bash
docker compose --profile caddy up -d --build
```

**Если nginx уже есть**, поднимите только CRM (она слушает `127.0.0.1:8080`) и подключите сайт из `deploy/server/nginx.conf`:
```bash
docker compose up -d --build
```
В nginx обязательно должны быть: `client_max_body_size 32m`, `proxy_request_buffering off`, `proxy_buffering off`, заголовки `Upgrade`/`Connection` для WebSocket, `proxy_read_timeout 2h`. Без них не будут работать загрузка частями, раздача файлов потоком, обновления в реальном времени и связь с нодой.

> **Мало памяти для сборки.** Сборке образа нужно примерно 1 ГБ RAM (npm + Go). Если на VPS меньше, соберите образ на компе и перенесите:
> ```bash
> # на компе, в папке langplant-crm
> docker build -f deploy/server/Dockerfile -t langplant-crm-server:latest .
> docker save langplant-crm-server:latest | gzip | ssh vps 'gunzip | docker load'
> # на VPS: docker compose up -d   (без --build)
> ```

### Вариант Б: один бинарник и systemd (минимум ресурсов)

```bash
# на машине со сборкой (нужны Go и Node), в папке langplant-crm
make dist                                   # dist/crm-server-linux-amd64 (и arm64)
scp dist/crm-server-linux-amd64 vps:/usr/local/bin/crm-server
scp deploy/server/crm-server.service vps:/etc/systemd/system/
# на VPS
useradd -r -s /usr/sbin/nologin -d /var/lib/langplant-crm crm
mkdir -p /var/lib/langplant-crm && chown crm: /var/lib/langplant-crm
cp .env /etc/langplant-crm.env && chmod 600 /etc/langplant-crm.env   # формат как в .env.example
systemctl daemon-reload && systemctl enable --now crm-server
```
Перед сервером так же нужен nginx или Caddy с TLS (см. выше).

### Первый вход

При первом запуске создаются 4 учётки, их пароли сохраняются в файл:
```bash
docker compose exec crm cat /data/initial-passwords.txt     # Docker
cat /var/lib/langplant-crm/initial-passwords.txt            # systemd
```
| Логин | Кто | Роль |
|---|---|---|
| `sasha` | Саша | администратор |
| `ksenia` | Ксения | участник |
| `anastasia` | Анастасия | участник |
| `isabella` | Изабелла | участник |

Передайте пароли участникам (каждый сменит свой в «Настройки → Профиль»), а потом **удалите файл**:
`docker compose exec crm rm /data/initial-passwords.txt`.

Управление пользователями из командной строки:
```bash
docker compose exec crm crm-server users                        # список
docker compose exec crm crm-server passwd ksenia                # новый случайный пароль
docker compose exec crm crm-server passwd ksenia 'свой-пароль'
docker compose exec crm crm-server useradd masha "Маша" [admin]
```

### Переменные окружения сервера

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `CRM_PUBLIC_URL` | — | `https://crm.example.com`; при https cookie ставятся с флагом Secure |
| `CRM_NODE_TOKEN` | — | секрет ноды, минимум 24 символа. Если пуст, нода не подключится |
| `CRM_ADDR` | `:8080` | адрес, на котором слушает сервер |
| `CRM_DATA_DIR` | `./data` (`/data` в Docker) | база, буфер, кэш, превью |
| `CRM_BUFFER_MAX` | `8GB` | сколько непереданных на комп файлов может лежать на VPS. Сверх лимита загрузки отклоняются с понятным сообщением |
| `CRM_CACHE_MAX` | `4GB` | локальные копии уже переданных файлов (LRU) |
| `CRM_PREVIEW_MAX` | `2GB` | лёгкие превью-видео (LRU) |
| `CRM_DISK_RESERVE` | `1GB` | сколько места на диске VPS всегда оставлять свободным |
| `CRM_WARM_MAX` | `128MB` | файлы до этого размера после просмотра кэшируются на VPS |
| `CRM_MAX_FILE` | `6GB` | максимальный размер одного файла (не больше `CRM_BUFFER_MAX`) |
| `CRM_CHUNK_SIZE` | `8MB` | размер куска загрузки; прокси должен пропускать тела такого размера |
| `CRM_TZ` | `Europe/Moscow` | часовой пояс плана публикаций (можно сменить в настройках) |

**Как подобрать лимиты.** Пусть на VPS свободно X ГБ. Тогда `CRM_BUFFER_MAX ≈ 0.5·X`, `CRM_CACHE_MAX + CRM_PREVIEW_MAX ≈ 0.3·X`, `CRM_DISK_RESERVE ≥ 1GB`.
Пока комп онлайн, буфер почти всегда пуст. Он заполняется, только если комп долго выключен.

---

## 2. Комп Саши (нода)

```bash
cd <repo>/langplant-crm/deploy/node
cp .env.example .env
# NODE_SERVER_URL=https://crm.example.com
# NODE_TOKEN=<то же, что CRM_NODE_TOKEN>
# STORAGE_PATH=D:/LangPlant        (Windows) | /Users/sasha/LangPlant (macOS) | /mnt/media/langplant (Linux)
docker compose up -d --build
docker compose logs -f        # должно появиться: "connected to server"
```

Контейнер с `restart: unless-stopped` поднимается вместе с Docker. В Docker Desktop включите «Start Docker Desktop when you sign in». Если соединение пропадает, нода переподключается сама (повторы с паузой от 1 до 30 секунд).

### Что лежит в `STORAGE_PATH`

```
blobs/ab/abcdef…       файлы, имя = sha256 содержимого (основной архив)
derived/…              превью, обложки (можно удалить — пересоздадутся)
backups/crm-*.db       снимки базы с VPS, каждые 6 ч, хранятся последние 60
trash/                 файлы, удалённые навсегда в CRM; хранятся NODE_TRASH_DAYS дней
tmp/                   недокачанные файлы (докачиваются)
node-id
```

Собрать обычные папки с роликами из бэкапа и архива (жёсткие ссылки, место не занимают):
```bash
docker compose run --rm crm-node export --out /storage/export
# → STORAGE_PATH/export/Ролики/LP-0042 Название/ru/Финальный ролик/final.mp4, …/Музыка/…
docker compose run --rm crm-node verify      # перепроверить sha256 всех файлов
```

### Переменные окружения ноды

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `NODE_SERVER_URL` | — | `https://crm.example.com` |
| `NODE_TOKEN` | — | равен `CRM_NODE_TOKEN` |
| `NODE_DATA_DIR` | `/storage` в Docker | папка архива (монтируется из `STORAGE_PATH`) |
| `NODE_PREVIEW_HEIGHT` | `960` | длинная сторона превью-видео |
| `NODE_DOWNLOADS` | `2` | сколько файлов одновременно забирать с VPS |
| `NODE_BACKUP_INTERVAL` | `6h` | как часто забирать бэкап базы |
| `NODE_BACKUP_KEEP` | `60` | сколько бэкапов хранить |
| `NODE_TRASH_DAYS` | `30` | сколько дней хранить окончательно удалённые файлы |

---

## 3. Проверка после установки

1. `curl https://crm.example.com/api/health` возвращает `{"ok":true,"node":true,…}`. `node:true` значит, что нода на связи.
2. Войти под `sasha`. В меню «Хранилище» горит зелёная точка, есть «Комп-хранилище на связи» и данные о диске компа.
3. Создать ролик и загрузить в «Финальный ролик» любой mp4. Через несколько секунд у файла появится отметка «на компе», а потом обложка в списке.
4. Открыть файл: проигрывается превью. Переключить на «Оригинал»: файл идёт с компа через VPS.
5. Через минуту после запуска ноды на странице «Хранилище» появится время последнего бэкапа, а файл окажется в `STORAGE_PATH/backups/`.
6. С телефона: открыть сайт, «Поделиться → На экран Домой». Приложение откроется в отдельном окне.

---

## 4. Обновление

```bash
git pull
cd langplant-crm/deploy/server && docker compose up -d --build     # VPS
cd langplant-crm/deploy/node   && docker compose up -d --build     # комп
```
Миграции базы применяются автоматически при старте. Загрузки, прерванные перезапуском, браузер докачивает сам.

## 5. Резервное копирование и восстановление

- **Файлы** лежат только на компе, в `STORAGE_PATH/blobs`. Их нужно бэкапить обычными средствами: второй диск, облако, Time Machine и т.п.
- **База** живёт на VPS и каждые 6 часов копируется на комп (`STORAGE_PATH/backups`).
- **Если VPS пропал:** поднять сервер заново, остановить его, положить свежий `crm-*.db` из `backups/` в том `/data` как `crm.db` и запустить снова. Нода подключится, и сервер сверит, какие файлы у неё есть.
  ```bash
  docker compose stop crm
  docker compose cp /path/to/backups/crm-20261005-120000.db crm:/data/crm.db
  # удалить журнал WAL от старой базы, иначе SQLite применит его к восстановленной
  docker compose run --rm --entrypoint sh crm -c 'rm -f /data/crm.db-wal /data/crm.db-shm'
  docker compose start crm
  ```

## 6. Если что-то не работает

| Симптом | Причина и что сделать |
|---|---|
| Нода пишет `server rejected NODE_TOKEN (401)` | `NODE_TOKEN` ≠ `CRM_NODE_TOKEN` |
| Нода пишет `bad handshake` или 400 на `/api/node/ws` | прокси не пропускает WebSocket: нужны `Upgrade`/`Connection` в nginx |
| Нода подключается и через минуту отваливается | у прокси слишком короткий `proxy_read_timeout`; должно быть ≥ 2 ч (нода шлёт ping каждые 20 с) |
| Загрузка падает на 413 | `client_max_body_size` меньше `CRM_CHUNK_SIZE` |
| Загрузка: «Буфер на сервере заполнен» | комп офлайн или лимит `CRM_BUFFER_MAX` мал. Включить комп или увеличить лимит |
| Файлы не открываются, «Хранилище офлайн» | файла нет в кэше VPS, а комп выключен. Это нормально, после включения компа всё откроется |
| Список обновляется только после F5 | прокси буферизует SSE (`/api/events`): `proxy_buffering off` |
| Нет обложек и превью | у ноды нет ffmpeg (в Docker-образе он есть) или файл повреждён. Подробности в логах ноды. Кнопка «Сверить» в «Хранилище» перезапускает обработку |
| «Потерянные файлы» в «Хранилище» | нода не нашла файл (удалили вручную или диск сменился). Загрузите оригинал заново, связь восстановится автоматически |

Логи: `docker compose logs -f crm` (VPS) и `docker compose logs -f crm-node` (комп).
