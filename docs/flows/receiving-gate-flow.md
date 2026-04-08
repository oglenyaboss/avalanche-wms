# Приемка на КПП (воротах)

## Суть

Принимаем грузоместа в рамках конкретной поставки (TTN). Грузоместа и их привязка к TTN уже известны из ERP и загружены в БД до начала сканирования.

## Диаграмма последовательности

```mermaid
sequenceDiagram
    autonumber
    actor Op as Оператор КПП
    participant UI as UI (Scanner)
    participant WMS as WMS API
    participant PG as Postgres

    Note over Op,PG: === Шаг 1: Сканирование ТТН ===
    Op->>UI: Сканирует штрихкод ТТН
    UI->>WMS: POST /receiving/gate/scan-ttn {ttn_code}
    WMS->>PG: SELECT shipment + cargoplaces WHERE ttn_code
    PG-->>WMS: shipment(CREATED) + список грузомест(EXPECTED)
    WMS->>PG: INSERT receiving_gate (action=SCAN_TTN)
    WMS->>PG: UPDATE shipment.status = GATE_IN_PROGRESS
    WMS-->>UI: Список ожидаемых грузомест

    Note over Op,PG: === Шаг 2: Сканирование грузомест (цикл) ===
    loop Для каждого грузоместа
        Op->>UI: Сканирует штрихкод грузоместа
        UI->>WMS: POST /receiving/gate/scan-cargoplace {shipment_id, cargoplace_code}
        WMS->>WMS: Проверка: грузоместо принадлежит этой TTN?
        WMS->>PG: UPDATE cargoplace.status = RECEIVED_AT_GATE, received_at_gate_at = now()
        WMS->>PG: INSERT receiving_gate (action=SCAN_CARGOPLACE)
        WMS-->>UI: Грузоместо принято ✓ (прогресс: 3/10)
    end

    Note over Op,PG: === Шаг 3: Завершение поставки ===
    alt Все грузоместа отсканированы
        Note over WMS: Автоматическое закрытие — все приняты
        WMS->>PG: UPDATE shipment.status = GATE_CLOSED
        WMS->>PG: INSERT receiving_gate (action=SHIPMENT_ACCEPTED)
        WMS-->>UI: Плашка "Поставка полностью принята" (10/10)
    else Есть неотсканированные грузоместа
        Op->>UI: Нажимает "Поставка принята"
        UI->>WMS: POST /receiving/gate/accept-shipment {shipment_id}
        WMS->>PG: UPDATE неотсканированные cargoplaces.status = NOT_RECEIVED
        WMS->>PG: UPDATE shipment.status = GATE_CLOSED
        WMS->>PG: INSERT receiving_gate (action=SHIPMENT_ACCEPTED)
        WMS-->>UI: Поставка закрыта (принято: 7, не принято: 3)
    end

    Note over WMS,PG: outbox_events НЕ создаются — КПП не пишет в блокчейн.<br/>Товары (products) ещё не существуют. Блокчейн-запись начинается на столе приёмки.
```

## Состояния сущностей

### inbound_shipments.status
```mermaid
stateDiagram-v2
    [*] --> CREATED : ERP загрузил данные
    CREATED --> GATE_IN_PROGRESS : Оператор отсканировал TTN
    GATE_IN_PROGRESS --> GATE_CLOSED : "Поставка принята" или все грузоместа отсканированы
```

### cargoplaces.status
```mermaid
stateDiagram-v2
    [*] --> EXPECTED : ERP загрузил данные
    EXPECTED --> RECEIVED_AT_GATE : Оператор отсканировал грузоместо
    EXPECTED --> NOT_RECEIVED : "Поставка принята" (не отсканировано)
    RECEIVED_AT_GATE --> TABLE_IN_PROGRESS : Передано на стол приемки
    RECEIVED_AT_GATE --> TABLE_CLOSED : Приемка на столе завершена
```

## Какие таблицы затрагиваются

| Таблица | Операция | Что меняется |
|---------|----------|-------------|
| `inbound_shipments` | UPDATE | status: CREATED → GATE_IN_PROGRESS → GATE_CLOSED |
| `cargoplaces` | UPDATE | status: EXPECTED → RECEIVED_AT_GATE / NOT_RECEIVED |
| `receiving_gate` | INSERT | Лог каждого действия (append-only) |
| `outbox_events` | — | **НЕ создаётся** (КПП не пишет в блокчейн, товары ещё не существуют) |
