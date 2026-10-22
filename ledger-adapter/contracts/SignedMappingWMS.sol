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
}
