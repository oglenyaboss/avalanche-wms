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


    function accept(uint256 eventId, uint256 itemId) external {
        _requireNewEvent(eventId);
        _transition(eventId, itemId, Status.None, Status.Accepted);
    }

    function putAway(uint256 eventId, uint256 itemId) external {
        _requireNewEvent(eventId);
        _transition(eventId, itemId, Status.Accepted, Status.PutAway);
    }

    function pick(uint256 eventId, uint256 itemId) external {
        _requireNewEvent(eventId);
        _transition(eventId, itemId, Status.PutAway, Status.Picked);
    }

    function ship(uint256 eventId, uint256 itemId) external {
        _requireNewEvent(eventId);
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


    function _batchTransition(
        uint256[] calldata eventIds,
        uint256[] calldata itemIds,
        Status requiredStatus,
        Status nextStatus
    ) internal {
        require(eventIds.length == itemIds.length, "Array length mismatch");
        require(eventIds.length > 0, "Empty arrays");

        for (uint256 i; i < eventIds.length; ) {
            _requireNewEvent(eventIds[i]);
            _transition(eventIds[i], itemIds[i], requiredStatus, nextStatus);
            unchecked { ++i; }
        }
    }

    function _requireNewEvent(uint256 eventId) internal {
        require(!processedEventIds[eventId], "Duplicate eventId");
        processedEventIds[eventId] = true;
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
