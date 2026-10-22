// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

library SignedAction {
    enum Type {
        None,
        Receive,
        PutAway,
        Pick,
        Ship,
        KeyRegister,
        KeyRevoke
    }

    enum State {
        None,
        Applied,
        Rejected
    }

    enum RejectReason {
        None,
        UnknownKey,
        KeyRevoked,
        BadSignature,
        WrongType,
        NotAdmin,
        KeyExists,
        InvalidPublicKey,
        WrongOrganization,
        TargetNotOperator,
        EmptyItems,
        ItemsNotAscending,
        WrongItemStatus
    }

    bytes32 internal constant TYPEHASH = keccak256("avalanche-wms/SignedAction/v1");
    bytes32 internal constant NO_ITEMS = keccak256("");

    function challenge(bytes16 actionId, Type actionType, bytes32 itemsHash, bytes32 detailsHash)
        internal
        pure
        returns (bytes32)
    {
        return keccak256(abi.encode(TYPEHASH, actionId, actionType, itemsHash, detailsHash));
    }

    function hashItems(uint256[] calldata itemIds) internal pure returns (bytes32) {
        return keccak256(abi.encodePacked(itemIds));
    }

    function keyId(bytes32 qx, bytes32 qy) internal pure returns (bytes32) {
        return keccak256(abi.encode(qx, qy));
    }
}
