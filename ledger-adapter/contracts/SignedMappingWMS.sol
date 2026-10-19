// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {WebAuthn} from "@openzeppelin/contracts/utils/cryptography/WebAuthn.sol";
import {ConsortiumRegistry} from "./ConsortiumRegistry.sol";
import {SignedAction} from "./SignedAction.sol";

contract SignedMappingWMS {
    enum Status {
        None,
        Accepted,
        PutAway,
        Picked,
        Shipped
    }

    struct Action {
        bytes16 actionId;
        SignedAction.Type actionType;
        uint256[] itemIds;
        bytes32 detailsHash;
        bytes32 keyId;
        WebAuthn.WebAuthnAuth auth;
    }

    ConsortiumRegistry public immutable registry;
    mapping(uint256 itemId => Status) public itemStatus;
    mapping(bytes16 actionId => SignedAction.State) public actionState;
    mapping(uint256 eventId => bool) public processedEventIds;

    event ActionApplied(
        bytes16 indexed actionId,
        bytes32 indexed keyId,
        bytes16 indexed orgId,
        SignedAction.Type actionType,
        bytes32 detailsHash,
        uint256 itemCount
    );
    event ItemMoved(uint256 indexed itemId, bytes16 indexed actionId, Status status);
    event ActionRejected(bytes16 indexed actionId, SignedAction.RejectReason reason, uint256 itemIndex);

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

    constructor(ConsortiumRegistry registry_) {
        registry = registry_;
    }

    function applyActions(Action[] calldata actions) external {
        for (uint256 i; i < actions.length; ++i) {
            _apply(actions[i]);
        }
    }

    function _apply(Action calldata a) private {
        if (actionState[a.actionId] != SignedAction.State.None) return;

        bytes32 challenge =
            SignedAction.challenge(a.actionId, a.actionType, SignedAction.hashItems(a.itemIds), a.detailsHash);
        (bytes16 orgId,, SignedAction.RejectReason reason) = registry.verifySigner(a.keyId, challenge, a.auth);
        if (reason != SignedAction.RejectReason.None) {
            emit ActionRejected(a.actionId, reason, 0);
            return;
        }

        (Status from, Status to, bool known) = _transition(a.actionType);
        uint256 badIndex;
        if (!known) {
            reason = SignedAction.RejectReason.WrongType;
        } else {
            (reason, badIndex) = _checkItems(a.itemIds, from);
        }
        if (reason != SignedAction.RejectReason.None) {
            actionState[a.actionId] = SignedAction.State.Rejected;
            emit ActionRejected(a.actionId, reason, badIndex);
            return;
        }

        actionState[a.actionId] = SignedAction.State.Applied;
        uint256[] calldata ids = a.itemIds;
        for (uint256 i; i < ids.length; ++i) {
            itemStatus[ids[i]] = to;
            emit ItemMoved(ids[i], a.actionId, to);
        }
        emit ActionApplied(a.actionId, a.keyId, orgId, a.actionType, a.detailsHash, ids.length);
    }

    function _checkItems(uint256[] calldata ids, Status from)
        private
        view
        returns (SignedAction.RejectReason, uint256)
    {
        if (ids.length == 0) {
            return (SignedAction.RejectReason.EmptyItems, 0);
        }
        for (uint256 i; i < ids.length; ++i) {
            if (i > 0 && ids[i] <= ids[i - 1]) return (SignedAction.RejectReason.ItemsNotAscending, i);
            if (itemStatus[ids[i]] != from) return (SignedAction.RejectReason.WrongItemStatus, i);
        }
        return (SignedAction.RejectReason.None, 0);
    }

    function _transition(SignedAction.Type t) private pure returns (Status from, Status to, bool known) {
        if (t == SignedAction.Type.Receive) return (Status.None, Status.Accepted, true);
        if (t == SignedAction.Type.PutAway) return (Status.Accepted, Status.PutAway, true);
        if (t == SignedAction.Type.Pick) return (Status.PutAway, Status.Picked, true);
        if (t == SignedAction.Type.Ship) return (Status.Picked, Status.Shipped, true);
        return (Status.None, Status.None, false);
    }

    // ---------- Legacy per-item / batch transitions ----------

    // Per-item функции СТРОГИЕ: дубликат eventId → no-op (early return), но неверный
    // item status → revert (через require в _transitionItem). Batch-функции МЯГКИЕ: оба
    // случая → skip. Адаптер ходит только через batch*; per-item оставлены для
    // единичных вызовов (тесты/CLI), где revert на неверный статус уместен.
    function accept(uint256 eventId, uint256 itemId) external {
        if (!_markEventIfNew(eventId)) return;
        _transitionItem(eventId, itemId, Status.None, Status.Accepted);
    }

    function putAway(uint256 eventId, uint256 itemId) external {
        if (!_markEventIfNew(eventId)) return;
        _transitionItem(eventId, itemId, Status.Accepted, Status.PutAway);
    }

    function pick(uint256 eventId, uint256 itemId) external {
        if (!_markEventIfNew(eventId)) return;
        _transitionItem(eventId, itemId, Status.PutAway, Status.Picked);
    }

    function ship(uint256 eventId, uint256 itemId) external {
        if (!_markEventIfNew(eventId)) return;
        _transitionItem(eventId, itemId, Status.Picked, Status.Shipped);
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

    function _transitionItem(
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
