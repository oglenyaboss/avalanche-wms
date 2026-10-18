// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {console} from "forge-std/console.sol";
import {BatchMappingWMS} from "../BatchMappingWMS.sol";
import {ConsortiumRegistry} from "../ConsortiumRegistry.sol";
import {SignedAction} from "../SignedAction.sol";
import {SignedMappingWMS} from "../SignedMappingWMS.sol";
import {PasskeyTest} from "../PasskeyTest.t.sol";

contract GasBench is PasskeyTest {
    ConsortiumRegistry registry;
    SignedMappingWMS wms;
    BatchMappingWMS legacy;
    Passkey admin;
    Passkey operator;
    uint128 nextAction;
    uint256 nextItem;

    function run() external {
        nextAction = uint128(vm.envOr("BENCH_SEED", uint256(1))) << 64;
        nextItem = uint256(keccak256(abi.encode("bench", nextAction))) >> 2;
        admin = makePasskey(0xA11CE);
        operator = makePasskey(0x0FE7A);
        uint256 probe = vm.envOr("PROBE_ITEMS", uint256(0));

        vm.startBroadcast();
        registry = new ConsortiumRegistry();
        wms = new SignedMappingWMS(registry);
        registry.registerOrganization(bytes16(nextAction), "Bench org", admin.qx, admin.qy);
        _registerOperator();

        if (probe > 0) {
            _signedPair(1, probe);
            vm.stopBroadcast();
            return;
        }

        legacy = new BatchMappingWMS();
        _signedPair(1, 1);
        _signedPair(1, 10);
        _signedPair(1, 100);
        _signedPair(1, 1000);
        _signedPair(10, 1);
        _signedPair(10, 10);
        _signedPair(10, 100);
        _legacyPair(10);
        _legacyPair(100);
        _legacyPair(1000);
        vm.stopBroadcast();
    }

    function _registerOperator() private {
        bytes16 id = bytes16(nextAction++);
        bytes32 ch = SignedAction.challenge(
            id, SignedAction.Type.KeyRegister, SignedAction.NO_ITEMS, keccak256(abi.encode(operator.qx, operator.qy))
        );
        ConsortiumRegistry.KeyAction[] memory batch = new ConsortiumRegistry.KeyAction[](1);
        batch[0] = ConsortiumRegistry.KeyAction(
            id, SignedAction.Type.KeyRegister, operator.qx, operator.qy, admin.keyId, signChallenge(admin, ch)
        );
        console.log("tx: key-register");
        registry.applyKeyActions(batch);
    }

    function _signedPair(uint256 actions, uint256 itemsPerAction) private {
        uint256[][] memory items = new uint256[][](actions);
        for (uint256 i; i < actions; ++i) {
            items[i] = _freshItems(itemsPerAction);
        }
        console.log("tx: signed receive actions=%d items=%d", actions, itemsPerAction);
        wms.applyActions(_batch(SignedAction.Type.Receive, items));
        console.log("tx: signed putaway actions=%d items=%d", actions, itemsPerAction);
        wms.applyActions(_batch(SignedAction.Type.PutAway, items));
    }

    function _legacyPair(uint256 n) private {
        uint256[] memory itemIds = _freshItems(n);
        uint256[] memory eventIds = new uint256[](n);
        for (uint256 i; i < n; ++i) {
            eventIds[i] = uint256(keccak256(abi.encode("event", itemIds[i])));
        }
        console.log("tx: legacy accept items=%d", n);
        legacy.batchAccept(eventIds, itemIds);
        for (uint256 i; i < n; ++i) {
            eventIds[i] = uint256(keccak256(abi.encode("event2", itemIds[i])));
        }
        console.log("tx: legacy putaway items=%d", n);
        legacy.batchPutAway(eventIds, itemIds);
    }

    function _batch(SignedAction.Type t, uint256[][] memory items)
        private
        returns (SignedMappingWMS.Action[] memory batch)
    {
        batch = new SignedMappingWMS.Action[](items.length);
        for (uint256 i; i < items.length; ++i) {
            bytes16 id = bytes16(nextAction++);
            bytes32 detailsHash = keccak256(abi.encode(id, "details"));
            bytes32 ch = SignedAction.challenge(id, t, keccak256(abi.encodePacked(items[i])), detailsHash);
            batch[i] =
                SignedMappingWMS.Action(id, t, items[i], detailsHash, operator.keyId, signChallenge(operator, ch));
        }
    }

    function _freshItems(uint256 n) private returns (uint256[] memory ids) {
        ids = new uint256[](n);
        for (uint256 i; i < n; ++i) {
            nextItem += 0x9e3779b97f4a7c15f39cc0605cedc8341082276bf3a27251f86c6a11d0c1;
            ids[i] = nextItem;
        }
    }
}
