# Ledger Adapter

HTTP-мост между WMS-сервисом и блокчейном Avalanche C-Chain. Принимает запросы от WMS, подписывает транзакции и отправляет их в смарт-контракт.

## Как это работает

```
WMS Service                Ledger Adapter              Avalanche C-Chain
    │                           │                              │
    │  POST /setstring          │                              │
    │  {"NewWord": "received"}  │                              │
    │ ─────────────────────────>│                              │
    │                           │  ethclient.Dial(RPC_URL)     │
    │                           │  ────────────────────────────>│
    │                           │  sign tx with PRIVATE_KEY    │
    │                           │  ────────────────────────────>│
    │                           │          tx receipt           │
    │                           │  <────────────────────────────│
    │   { tx_hash, gas_used }   │                              │
    │ <─────────────────────────│                              │
```

1. WMS Service выполняет складскую операцию (приёмка, сборка и т.д.)
2. Отправляет HTTP-запрос в Ledger Adapter с данными операции
3. Ledger Adapter подписывает транзакцию приватным ключом
4. Отправляет подписанную транзакцию в Avalanche C-Chain через JSON-RPC
5. Возвращает результат (tx hash, gas used) обратно в WMS

## Структура

```
ledger-adapter/
├── cmd/
│   └── adapter/
│       └── main.go              # Точка входа (тонкий: конфиг → клиент → handler → сервер)
│
├── internal/
│   ├── config/
│   │   └── config.go            # Загрузка из env: RPC_URL, PRIVATE_KEY, CONTRACT_ADDR
│   │
│   ├── chain/
│   │   ├── client.go            # Обёртка над ethclient: подключение, подпись, отправка tx
│   │   └── abi.go               # ABI смарт-контракта (JSON)
│   │
│   └── handler/
│       └── handler.go           # HTTP-обработчики для всех эндпоинтов
│
├── contracts/
│   └── contract_copy.sol        # Solidity-контракт (для справки)
│
├── Dockerfile
├── go.mod
└── .env.example
```

### Почему go-ethereum для Avalanche?

Avalanche C-Chain — это EVM-совместимая цепочка. Она поддерживает тот же JSON-RPC API, что и Ethereum. Поэтому библиотека [go-ethereum](https://github.com/ethereum/go-ethereum) работает с Avalanche без изменений — меняется только RPC URL.

> Подробнее: [Avalanche C-Chain EVM](https://docs.avax.network/build/dapp/c-chain-evm)

## API

### `GET /health`

Проверка что сервис запущен.

```json
{
  "status": "healty",
  "time": "2026-03-01T14:30:00+05:00"
}
```

### `GET /`

Получить текущее сообщение из смарт-контракта.

```json
{
  "message": "Hello World"
}
```

### `POST /setstring`

Записать новое сообщение в контракт (перезаписывает текущее).

**Запрос:**
```json
{
  "NewWord": "shipment_12345_received"
}
```

**Ответ:** информация о транзакции (gas used, tx hash и т.д.)

### `POST /addfunc`

Добавить сообщение к существующему (конкатенация).

**Запрос:**
```json
{
  "addition": "_confirmed"
}
```

### `GET /viewlogs?id=N`

Получить логи (события) контракта начиная с блока N.

```json
[
  {
    "address": "0x...",
    "data": "new value set",
    "blockNumber": 12345,
    "transactionHash": "0x...",
    "removed": false
  }
]
```

## Конфигурация

| Переменная | Описание | По умолчанию |
|------------|----------|-------------|
| `PORT` | Порт HTTP-сервера | `8085` |
| `RPC_URL` | Avalanche C-Chain RPC endpoint | `http://localhost:8545` |
| `PRIVATE_KEY` | Приватный ключ для подписи транзакций (hex, без `0x`) | — |
| `CONTRACT_ADDR` | Адрес развёрнутого смарт-контракта | — |

### Как получить значения для конфигурации

1. **RPC_URL** — для тестнета Avalanche Fuji: `https://api.avax-test.network/ext/bc/C/rpc`
2. **PRIVATE_KEY** — экспортировать из MetaMask: Настройки → Безопасность → Экспорт приватного ключа
3. **CONTRACT_ADDR** — получается после деплоя контракта (Remix / Hardhat / Foundry)

> Никогда не коммитьте `.env` с реальными ключами! Используйте `.env.example` как шаблон.

## Смарт-контракт

Контракт лежит в `contracts/contract_copy.sol`. Основные функции:

| Функция | Тип | Описание |
|---------|-----|----------|
| `message()` | view | Получить текущее сообщение |
| `setMessage(string)` | write | Установить новое сообщение |
| `addMessage(string)` | write | Добавить к текущему сообщению |

Каждая write-операция генерирует событие `setlogs(string)` — его можно прочитать через `/viewlogs`.

## Локальная разработка

```bash
# Из корня проекта
make lint-ledger     # линтинг
make test-ledger     # тесты
make tidy-ledger     # go mod tidy
make vendor-ledger   # обновить vendor
```

### Без Docker

```bash
export RPC_URL=https://api.avax-test.network/ext/bc/C/rpc
export PRIVATE_KEY=ваш_приватный_ключ
export CONTRACT_ADDR=адрес_контракта

cd ledger-adapter
go run ./cmd/adapter/
```

## Зависимости

| Пакет | Назначение | Документация |
|-------|-----------|--------------|
| [go-ethereum](https://github.com/ethereum/go-ethereum) | EVM-клиент (ethclient, ABI, crypto) | [go-ethereum book](https://goethereumbook.org/) |

### Ключевые пакеты go-ethereum

- `ethclient` — JSON-RPC клиент к EVM-ноде
- `accounts/abi` — парсинг и кодирование ABI
- `crypto` — работа с приватными ключами, подпись транзакций
- `common` — типы Address, Hash
- `core/types` — Transaction, Receipt, Log

## Полезные ссылки

- [Avalanche C-Chain Docs](https://docs.avax.network/build/dapp/c-chain-evm) — документация EVM на Avalanche
- [Go Ethereum Book](https://goethereumbook.org/) — пошаговый гайд по go-ethereum (работает и для Avalanche)
- [Remix IDE](https://remix.ethereum.org/) — деплой контрактов через браузер
- [Avalanche Fuji Faucet](https://faucet.avax.network/) — тестовые AVAX для Fuji testnet
- [Solidity by Example](https://solidity-by-example.org/) — примеры Solidity-контрактов
