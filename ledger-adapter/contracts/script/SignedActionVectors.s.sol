// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {WebAuthn} from "@openzeppelin/contracts/utils/cryptography/WebAuthn.sol";
import {SignedAction} from "../SignedAction.sol";
import {PasskeyTest} from "../PasskeyTest.t.sol";

contract SignedActionVectors is PasskeyTest {
    string constant OUT = "../../testdata/signed-actions/vectors.v1.json";

    bytes16 constant ORG = 0x01929c5e6f00700080000000000000a1;
    bytes16 constant OPERATOR = 0x01929c5e6f00700080000000000000b1;
    bytes16 constant CARGOPLACE = 0x01929c5e6f00700080000000000000c1;
    bytes16 constant STORAGE_BIN = 0x01929c5e6f00700080000000000000d1;
    bytes16 constant SHIPPING_BUFFER = 0x01929c5e6f00700080000000000000d2;
    bytes16 constant DISPATCH = 0x01929c5e6f00700080000000000000e1;
    bytes16 constant ORDER_1 = 0x01929c5e6f00700080000000000000f1;
    bytes16 constant ORDER_2 = 0x01929c5e6f00700080000000000000f2;
    uint64 constant T0 = 1760781600000;

    Passkey signer;
    Passkey subject;
    string[] productIds;
    uint256[] itemIds;

    function run() external {
        signer = makePasskey(uint256(keccak256("avalanche-wms/test-vectors/admin-passkey")) % P256_N);
        subject = makePasskey(uint256(keccak256("avalanche-wms/test-vectors/operator-passkey")) % P256_N);
        _items();

        string[] memory actions = new string[](6);
        bytes32 h;
        string memory d;
        actions[0] = _keyAction("keyRegister", 0x01929c5e6f0070008000000000001001, SignedAction.Type.KeyRegister);
        (h, d) = _receiveDetails("receive", keccak256("salt/receive"), T0);
        actions[1] = _fsmAction("receive", 0x01929c5e6f0070008000000000001002, SignedAction.Type.Receive, h, d);
        (h, d) = _putAwayDetails("putAway", keccak256("salt/putAway"), T0 + 60_000);
        actions[2] = _fsmAction("putAway", 0x01929c5e6f0070008000000000001003, SignedAction.Type.PutAway, h, d);
        (h, d) = _pickDetails("pick", keccak256("salt/pick"), T0 + 120_000);
        actions[3] = _fsmAction("pick", 0x01929c5e6f0070008000000000001004, SignedAction.Type.Pick, h, d);
        (h, d) = _shipDetails("ship", keccak256("salt/ship"), T0 + 180_000);
        actions[4] = _fsmAction("ship", 0x01929c5e6f0070008000000000001005, SignedAction.Type.Ship, h, d);
        actions[5] = _keyAction("keyRevoke", 0x01929c5e6f0070008000000000001006, SignedAction.Type.KeyRevoke);

        string memory root = "root";
        vm.serializeString(root, "version", "avalanche-wms/SignedAction/v1");
        vm.serializeBytes32(root, "typehash", SignedAction.TYPEHASH);
        vm.serializeBytes32(root, "noItemsHash", SignedAction.NO_ITEMS);
        vm.serializeString(root, "orgId", uuid(ORG));
        vm.serializeString(root, "signer", _key("signer", signer, true));
        vm.serializeString(root, "subjectKey", _key("subjectKey", subject, false));
        vm.serializeString(root, "products", _products());
        string memory json = vm.serializeString(root, "actions", actions);
        vm.writeJson(json, OUT);
    }

    function _items() private {
        string[3] memory ids = [
            "01929c5e-6f00-7000-8000-000000000101",
            "01929c5e-6f00-7000-8000-000000000102",
            "01929c5e-6f00-7000-8000-000000000103"
        ];
        uint256[3] memory hashed;
        for (uint256 i; i < ids.length; ++i) {
            hashed[i] = uint256(keccak256(bytes(ids[i])));
            for (uint256 j = i; j > 0 && hashed[j - 1] > hashed[j]; --j) {
                (hashed[j], hashed[j - 1]) = (hashed[j - 1], hashed[j]);
                (ids[j], ids[j - 1]) = (ids[j - 1], ids[j]);
            }
        }
        for (uint256 i; i < ids.length; ++i) {
            itemIds.push(hashed[i]);
            productIds.push(ids[i]);
        }
    }

    function _products() private returns (string[] memory out) {
        out = new string[](itemIds.length);
        for (uint256 i; i < itemIds.length; ++i) {
            string memory k = string.concat("product", vm.toString(i));
            vm.serializeString(k, "productId", productIds[i]);
            out[i] = vm.serializeBytes32(k, "itemId", bytes32(itemIds[i]));
        }
    }

    function _receiveDetails(string memory k, bytes32 salt, uint64 t) private returns (bytes32, string memory) {
        string memory o = string.concat(k, ".details");
        vm.serializeBytes32(o, "salt", salt);
        vm.serializeString(o, "cargoplaceId", uuid(CARGOPLACE));
        vm.serializeString(o, "operatorId", uuid(OPERATOR));
        string memory json = vm.serializeUint(o, "occurredAtMs", t);
        return (keccak256(abi.encode(salt, CARGOPLACE, OPERATOR, t)), json);
    }

    function _putAwayDetails(string memory k, bytes32 salt, uint64 t) private returns (bytes32, string memory) {
        string memory o = string.concat(k, ".details");
        vm.serializeBytes32(o, "salt", salt);
        vm.serializeString(o, "storageBinId", uuid(STORAGE_BIN));
        vm.serializeString(o, "operatorId", uuid(OPERATOR));
        string memory json = vm.serializeUint(o, "occurredAtMs", t);
        return (keccak256(abi.encode(salt, STORAGE_BIN, OPERATOR, t)), json);
    }

    function _pickDetails(string memory k, bytes32 salt, uint64 t) private returns (bytes32, string memory) {
        string memory o = string.concat(k, ".details");
        vm.serializeBytes32(o, "salt", salt);
        vm.serializeString(o, "shippingBufferBinId", uuid(SHIPPING_BUFFER));
        vm.serializeString(o, "orderIds", _orderStrings());
        vm.serializeString(o, "operatorId", uuid(OPERATOR));
        string memory json = vm.serializeUint(o, "occurredAtMs", t);
        return (keccak256(abi.encode(salt, SHIPPING_BUFFER, _orders(), OPERATOR, t)), json);
    }

    function _shipDetails(string memory k, bytes32 salt, uint64 t) private returns (bytes32, string memory) {
        string memory o = string.concat(k, ".details");
        vm.serializeBytes32(o, "salt", salt);
        vm.serializeString(o, "dispatchId", uuid(DISPATCH));
        vm.serializeString(o, "shippingBufferBinId", uuid(SHIPPING_BUFFER));
        vm.serializeString(o, "orderIds", _orderStrings());
        vm.serializeString(o, "operatorId", uuid(OPERATOR));
        string memory json = vm.serializeUint(o, "occurredAtMs", t);
        return (keccak256(abi.encode(salt, DISPATCH, SHIPPING_BUFFER, _orders(), OPERATOR, t)), json);
    }

    function _fsmAction(
        string memory k,
        bytes16 actionId,
        SignedAction.Type t,
        bytes32 detailsHash,
        string memory details
    ) private returns (string memory) {
        bytes32 itemsHash = keccak256(abi.encodePacked(itemIds));
        bytes32 ch = SignedAction.challenge(actionId, t, itemsHash, detailsHash);
        bytes32[] memory ids = new bytes32[](itemIds.length);
        for (uint256 i; i < ids.length; ++i) {
            ids[i] = bytes32(itemIds[i]);
        }
        vm.serializeBytes32(k, "itemIds", ids);
        vm.serializeString(k, "details", details);
        return _envelope(k, actionId, t, itemsHash, detailsHash, ch);
    }

    function _keyAction(string memory k, bytes16 actionId, SignedAction.Type t) private returns (string memory) {
        bytes32 detailsHash = keccak256(abi.encode(subject.qx, subject.qy));
        bytes32 ch = SignedAction.challenge(actionId, t, SignedAction.NO_ITEMS, detailsHash);
        vm.serializeBytes32(k, "itemIds", new bytes32[](0));
        string memory o = string.concat(k, ".details");
        vm.serializeBytes32(o, "qx", subject.qx);
        vm.serializeString(k, "details", vm.serializeBytes32(o, "qy", subject.qy));
        return _envelope(k, actionId, t, SignedAction.NO_ITEMS, detailsHash, ch);
    }

    function _envelope(
        string memory k,
        bytes16 actionId,
        SignedAction.Type t,
        bytes32 itemsHash,
        bytes32 detailsHash,
        bytes32 ch
    ) private returns (string memory) {
        vm.serializeString(k, "name", k);
        vm.serializeString(k, "actionId", uuid(actionId));
        vm.serializeBytes(k, "actionIdBytes16", abi.encodePacked(actionId));
        vm.serializeUint(k, "actionType", uint8(t));
        vm.serializeBytes32(k, "itemsHash", itemsHash);
        vm.serializeBytes32(k, "detailsHash", detailsHash);
        vm.serializeBytes32(k, "challenge", ch);
        vm.serializeBytes32(k, "keyId", signer.keyId);
        return vm.serializeString(k, "webauthn", _auth(string.concat(k, ".webauthn"), signChallenge(signer, ch)));
    }

    function _auth(string memory o, WebAuthn.WebAuthnAuth memory a) private returns (string memory) {
        vm.serializeBytes(o, "authenticatorData", a.authenticatorData);
        vm.serializeBytes(o, "clientDataJSONHex", bytes(a.clientDataJSON));
        vm.serializeUint(o, "challengeIndex", a.challengeIndex);
        vm.serializeUint(o, "typeIndex", a.typeIndex);
        vm.serializeBytes32(o, "r", a.r);
        return vm.serializeBytes32(o, "s", a.s);
    }

    function _key(string memory o, Passkey memory p, bool withPrivateKey) private returns (string memory) {
        if (withPrivateKey) vm.serializeBytes32(o, "privateKey", bytes32(p.privateKey));
        vm.serializeBytes32(o, "qx", p.qx);
        vm.serializeBytes32(o, "qy", p.qy);
        return vm.serializeBytes32(o, "keyId", p.keyId);
    }

    function _orders() private pure returns (bytes16[] memory o) {
        o = new bytes16[](2);
        o[0] = ORDER_1;
        o[1] = ORDER_2;
    }

    function _orderStrings() private pure returns (string[] memory o) {
        o = new string[](2);
        o[0] = uuid(ORDER_1);
        o[1] = uuid(ORDER_2);
    }

    function uuid(bytes16 b) internal pure returns (string memory) {
        bytes memory hex_ = bytes(vm.toString(abi.encodePacked(b)));
        bytes memory out = new bytes(36);
        uint256 j = 2;
        for (uint256 i; i < 36; ++i) {
            if (i == 8 || i == 13 || i == 18 || i == 23) {
                out[i] = "-";
            } else {
                out[i] = hex_[j++];
            }
        }
        return string(out);
    }
}
