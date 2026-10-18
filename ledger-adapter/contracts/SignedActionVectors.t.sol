// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {Test} from "forge-std/Test.sol";
import {WebAuthn} from "@openzeppelin/contracts/utils/cryptography/WebAuthn.sol";
import {ConsortiumRegistry} from "./ConsortiumRegistry.sol";
import {SignedAction} from "./SignedAction.sol";
import {SignedMappingWMS} from "./SignedMappingWMS.sol";

contract SignedActionVectorsTest is Test {
    string json;
    ConsortiumRegistry registry;
    SignedMappingWMS wms;

    function setUp() public {
        json = vm.readFile("../../testdata/signed-actions/vectors.v1.json");
        registry = new ConsortiumRegistry();
        wms = new SignedMappingWMS(registry);
        bytes16 orgId = bytes16(uint128(0));
        registry.registerOrganization(orgId, "vectors", _b32(".signer.qx"), _b32(".signer.qy"));
    }

    function test_HeaderMatchesContract() public view {
        assertEq(_b32(".typehash"), SignedAction.TYPEHASH);
        assertEq(_b32(".noItemsHash"), SignedAction.NO_ITEMS);
        assertEq(_b32(".signer.keyId"), keccak256(abi.encode(_b32(".signer.qx"), _b32(".signer.qy"))));
    }

    function test_EveryVectorIsAcceptedByContracts() public {
        uint256 n = abi.decode(vm.parseJson(json, ".actions"), (bytes[])).length;
        for (uint256 i; i < n; ++i) {
            string memory p = string.concat(".actions[", vm.toString(i), "]");
            bytes16 actionId = bytes16(vm.parseJsonBytes(json, string.concat(p, ".actionIdBytes16")));
            SignedAction.Type t = SignedAction.Type(vm.parseJsonUint(json, string.concat(p, ".actionType")));
            bytes32[] memory raw = vm.parseJsonBytes32Array(json, string.concat(p, ".itemIds"));
            uint256[] memory itemIds = new uint256[](raw.length);
            for (uint256 j; j < raw.length; ++j) {
                itemIds[j] = uint256(raw[j]);
            }
            bytes32 itemsHash = keccak256(abi.encodePacked(itemIds));
            assertEq(itemsHash, _b32(string.concat(p, ".itemsHash")), p);
            bytes32 detailsHash = _b32(string.concat(p, ".detailsHash"));
            assertEq(
                SignedAction.challenge(actionId, t, itemsHash, detailsHash), _b32(string.concat(p, ".challenge")), p
            );

            WebAuthn.WebAuthnAuth memory auth = _auth(string.concat(p, ".webauthn"));
            bytes32 keyId = _b32(string.concat(p, ".keyId"));
            if (t == SignedAction.Type.KeyRegister || t == SignedAction.Type.KeyRevoke) {
                bytes32 qx = _b32(string.concat(p, ".details.qx"));
                bytes32 qy = _b32(string.concat(p, ".details.qy"));
                assertEq(detailsHash, keccak256(abi.encode(qx, qy)), p);
                ConsortiumRegistry.KeyAction[] memory batch = new ConsortiumRegistry.KeyAction[](1);
                batch[0] = ConsortiumRegistry.KeyAction(actionId, t, qx, qy, keyId, auth);
                registry.applyKeyActions(batch);
                assertTrue(registry.actionState(actionId) == SignedAction.State.Applied, p);
            } else {
                SignedMappingWMS.Action[] memory batch = new SignedMappingWMS.Action[](1);
                batch[0] = SignedMappingWMS.Action(actionId, t, itemIds, detailsHash, keyId, auth);
                wms.applyActions(batch);
                assertTrue(wms.actionState(actionId) == SignedAction.State.Applied, p);
            }
        }
    }

    function _auth(string memory p) private view returns (WebAuthn.WebAuthnAuth memory) {
        return WebAuthn.WebAuthnAuth(
            _b32(string.concat(p, ".r")),
            _b32(string.concat(p, ".s")),
            vm.parseJsonUint(json, string.concat(p, ".challengeIndex")),
            vm.parseJsonUint(json, string.concat(p, ".typeIndex")),
            vm.parseJsonBytes(json, string.concat(p, ".authenticatorData")),
            string(vm.parseJsonBytes(json, string.concat(p, ".clientDataJSONHex")))
        );
    }

    function _b32(string memory path) private view returns (bytes32) {
        return vm.parseJsonBytes32(json, path);
    }
}
