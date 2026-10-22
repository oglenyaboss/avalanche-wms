// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {P256} from "@openzeppelin/contracts/utils/cryptography/P256.sol";
import {WebAuthn} from "@openzeppelin/contracts/utils/cryptography/WebAuthn.sol";
import {SignedAction} from "./SignedAction.sol";

contract ConsortiumRegistry {
    enum Role {
        None,
        Admin,
        Operator
    }

    struct Key {
        bytes32 qx;
        bytes32 qy;
        bytes16 orgId;
        Role role;
        bool active;
    }

    struct KeyAction {
        bytes16 actionId;
        SignedAction.Type actionType;
        bytes32 qx;
        bytes32 qy;
        bytes32 signerKeyId;
        WebAuthn.WebAuthnAuth auth;
    }

    // ponytail: владелец задаётся деплоем и не передаётся; при смене владельца нужна двухшаговая передача.
    address public immutable owner;
    mapping(bytes16 orgId => bool) public organizationExists;
    mapping(bytes32 keyId => Key) private _keys;
    mapping(bytes16 actionId => SignedAction.State) public actionState;

    event OrganizationRegistered(bytes16 indexed orgId, string name);
    event KeyRegistered(
        bytes32 indexed keyId, bytes16 indexed orgId, Role role, bytes32 qx, bytes32 qy, bytes16 actionId
    );
    event KeyRevoked(bytes32 indexed keyId, bytes16 indexed orgId, bytes16 actionId);
    event ActionRejected(bytes16 indexed actionId, SignedAction.RejectReason reason);

    error NotOwner();
    error OrganizationExists(bytes16 orgId);
    error UnknownOrganization(bytes16 orgId);
    error KeyAlreadyRegistered(bytes32 keyId);
    error InvalidPublicKey();
    error UnknownKey(bytes32 keyId);

    modifier onlyOwner() {
        if (msg.sender != owner) revert NotOwner();
        _;
    }

    constructor() {
        owner = msg.sender;
    }

    function registerOrganization(bytes16 orgId, string calldata name, bytes32 adminQx, bytes32 adminQy)
        external
        onlyOwner
    {
        if (organizationExists[orgId]) revert OrganizationExists(orgId);
        organizationExists[orgId] = true;
        emit OrganizationRegistered(orgId, name);
        _registerAdminKey(orgId, adminQx, adminQy);
    }

    function registerAdminKey(bytes16 orgId, bytes32 qx, bytes32 qy) external onlyOwner {
        if (!organizationExists[orgId]) revert UnknownOrganization(orgId);
        _registerAdminKey(orgId, qx, qy);
    }

    function revokeKey(bytes32 keyId) external onlyOwner {
        Key storage key = _keys[keyId];
        if (key.role == Role.None) revert UnknownKey(keyId);
        if (!key.active) return;
        key.active = false;
        emit KeyRevoked(keyId, key.orgId, 0);
    }

    function applyKeyActions(KeyAction[] calldata actions) external {
        for (uint256 i; i < actions.length; ++i) {
            _applyKeyAction(actions[i]);
        }
    }

    function getKey(bytes32 keyId) external view returns (Key memory) {
        return _keys[keyId];
    }

    function verifySigner(bytes32 keyId, bytes32 challenge, WebAuthn.WebAuthnAuth calldata auth)
        public
        view
        returns (bytes16 orgId, Role role, SignedAction.RejectReason reason)
    {
        Key storage key = _keys[keyId];
        role = key.role;
        orgId = key.orgId;
        if (role == Role.None) return (orgId, role, SignedAction.RejectReason.UnknownKey);
        if (!key.active) return (orgId, role, SignedAction.RejectReason.KeyRevoked);
        if (!WebAuthn.verify(abi.encodePacked(challenge), auth, key.qx, key.qy, true)) {
            return (orgId, role, SignedAction.RejectReason.BadSignature);
        }
        return (orgId, role, SignedAction.RejectReason.None);
    }

    function _registerAdminKey(bytes16 orgId, bytes32 qx, bytes32 qy) private {
        bytes32 keyId = SignedAction.keyId(qx, qy);
        if (_keys[keyId].role != Role.None) revert KeyAlreadyRegistered(keyId);
        if (!P256.isValidPublicKey(qx, qy)) revert InvalidPublicKey();
        _keys[keyId] = Key(qx, qy, orgId, Role.Admin, true);
        emit KeyRegistered(keyId, orgId, Role.Admin, qx, qy, 0);
    }

    function _applyKeyAction(KeyAction calldata a) private {
        if (actionState[a.actionId] != SignedAction.State.None) return;

        bytes32 challenge =
            SignedAction.challenge(a.actionId, a.actionType, SignedAction.NO_ITEMS, keccak256(abi.encode(a.qx, a.qy)));
        (bytes16 orgId, Role signerRole, SignedAction.RejectReason reason) =
            verifySigner(a.signerKeyId, challenge, a.auth);
        if (reason != SignedAction.RejectReason.None) {
            emit ActionRejected(a.actionId, reason);
            return;
        }

        reason = _keyActionEffect(a, orgId, signerRole);
        if (reason == SignedAction.RejectReason.None) {
            actionState[a.actionId] = SignedAction.State.Applied;
        } else {
            actionState[a.actionId] = SignedAction.State.Rejected;
            emit ActionRejected(a.actionId, reason);
        }
    }

    function _keyActionEffect(KeyAction calldata a, bytes16 orgId, Role signerRole)
        private
        returns (SignedAction.RejectReason)
    {
        if (signerRole != Role.Admin) return SignedAction.RejectReason.NotAdmin;
        bytes32 target = SignedAction.keyId(a.qx, a.qy);

        if (a.actionType == SignedAction.Type.KeyRegister) {
            if (_keys[target].role != Role.None) return SignedAction.RejectReason.KeyExists;
            if (!P256.isValidPublicKey(a.qx, a.qy)) return SignedAction.RejectReason.InvalidPublicKey;
            _keys[target] = Key(a.qx, a.qy, orgId, Role.Operator, true);
            emit KeyRegistered(target, orgId, Role.Operator, a.qx, a.qy, a.actionId);
            return SignedAction.RejectReason.None;
        }

        if (a.actionType == SignedAction.Type.KeyRevoke) {
            Key storage key = _keys[target];
            if (key.role == Role.None) return SignedAction.RejectReason.UnknownKey;
            if (key.orgId != orgId) return SignedAction.RejectReason.WrongOrganization;
            if (key.role != Role.Operator) return SignedAction.RejectReason.TargetNotOperator;
            if (!key.active) return SignedAction.RejectReason.KeyRevoked;
            key.active = false;
            emit KeyRevoked(target, orgId, a.actionId);
            return SignedAction.RejectReason.None;
        }

        return SignedAction.RejectReason.WrongType;
    }
}
