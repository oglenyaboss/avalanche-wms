// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

contract BatchMappingWMS {
    enum Status { None, Accepted, PutAway, Picked, Shipped }

    mapping(uint256 => Status) public itemStatus;
    mapping(uint256 => bool)   public processedEventIds;

    event ItemTransition(
        uint256 indexed eventId,
        uint256 indexed itemId,
        Status previousStatus,
        Status nextStatus,
        address actor,
        uint256 timestamp
    );

    // ItemTransitionFailed эмитится когда элемент batch'а пропущен из-за неверного
    // item status (#47/S3): транзакция НЕ ревертится, валидные siblings проходят,
    // а пропуск остаётся наблюдаемым on-chain для chain-status gate (#45) / reconcile.
    event ItemTransitionFailed(
        uint256 indexed eventId,
        uint256 indexed itemId,
        Status actualStatus,
        Status expectedStatus
    );


    function accept(uint256 eventId, uint256 itemId) external {
        if (!_markEventIfNew(eventId)) return;
        _transition(eventId, itemId, Status.None, Status.Accepted);
    }

    function putAway(uint256 eventId, uint256 itemId) external {
        if (!_markEventIfNew(eventId)) return;
        _transition(eventId, itemId, Status.Accepted, Status.PutAway);
    }

    function pick(uint256 eventId, uint256 itemId) external {
        if (!_markEventIfNew(eventId)) return;
        _transition(eventId, itemId, Status.PutAway, Status.Picked);
    }

    function ship(uint256 eventId, uint256 itemId) external {
        if (!_markEventIfNew(eventId)) return;
        _transition(eventId, itemId, Status.Picked, Status.Shipped);
    }


    function batchAccept(uint256[] calldata eventIds, uint256[] calldata itemIds) external {
        _batchTransition(eventIds, itemIds, Status.None, Status.Accepted);
    }

    function batchPutAway(uint256[] calldata eventIds, uint256[] calldata itemIds) external {
        _batchTransition(eventIds, itemIds, Status.Accepted, Status.PutAway);
    }

    function batchPick(uint256[] calldata eventIds, uint256[] calldata itemIds) external {
        _batchTransition(eventIds, itemIds, Status.PutAway, Status.Picked);
    }

    function batchShip(uint256[] calldata eventIds, uint256[] calldata itemIds) external {
        _batchTransition(eventIds, itemIds, Status.Picked, Status.Shipped);
    }


    // _batchTransition обрабатывает каждый элемент независимо: ни дубликат eventId,
    // ни неверный item status НЕ ревертят весь batch (Kafka at-least-once гарантирует
    // дубликаты; один плохой item не должен ронять валидные siblings — #44, #47).
    function _batchTransition(
        uint256[] calldata eventIds,
        uint256[] calldata itemIds,
        Status requiredStatus,
        Status nextStatus
    ) internal {
        require(eventIds.length == itemIds.length, "Array length mismatch");
        require(eventIds.length > 0, "Empty arrays");

        for (uint256 i; i < eventIds.length; ) {
            uint256 eventId = eventIds[i];
            uint256 itemId = itemIds[i];

            // #44: дубликат eventId (cross-tx redelivery или повтор внутри batch) → skip.
            if (!_markEventIfNew(eventId)) {
                unchecked { ++i; }
                continue;
            }

            // #47/S3: неверный item status — пропускаем ТОЛЬКО этот элемент с
            // наблюдаемым событием, не ревертим транзакцию.
            Status current = itemStatus[itemId];
            if (current != requiredStatus) {
                emit ItemTransitionFailed(eventId, itemId, current, requiredStatus);
                unchecked { ++i; }
                continue;
            }

            itemStatus[itemId] = nextStatus;
            emit ItemTransition(eventId, itemId, requiredStatus, nextStatus, msg.sender, block.timestamp);
            unchecked { ++i; }
        }
    }

    // _markEventIfNew атомарно проверяет-и-помечает eventId. Возвращает false если
    // событие уже обработано (replay) — вызывающий должен сделать no-op, НЕ revert.
    // Replay-защита сохраняется: каждый eventId исполняется ровно один раз.
    function _markEventIfNew(uint256 eventId) internal returns (bool isNew) {
        if (processedEventIds[eventId]) {
            return false;
        }
        processedEventIds[eventId] = true;
        return true;
    }

    function _transition(
        uint256 eventId,
        uint256 itemId,
        Status requiredStatus,
        Status nextStatus
    ) internal {
        Status current = itemStatus[itemId];
        require(current == requiredStatus, "Invalid status transition");

        itemStatus[itemId] = nextStatus;

        emit ItemTransition(
            eventId, itemId,
            requiredStatus, nextStatus,
            msg.sender, block.timestamp
        );
    }
}
