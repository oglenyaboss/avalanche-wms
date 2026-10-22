// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {WebAuthn} from "@openzeppelin/contracts/utils/cryptography/WebAuthn.sol";
import {ConsortiumRegistry} from "./ConsortiumRegistry.sol";
import {SignedAction} from "./SignedAction.sol";
import {SignedMappingWMS} from "./SignedMappingWMS.sol";
import {PasskeyTest} from "./PasskeyTest.t.sol";

contract SignedActionsTest is PasskeyTest {
    ConsortiumRegistry registry;
    SignedMappingWMS wms;

    bytes16 constant ORG_A = bytes16(0x0190f2a0000070008000000000000a0a);
    bytes16 constant ORG_B = bytes16(0x0190f2a0000070008000000000000b0b);
    bytes16 constant ORG_C = bytes16(0x0190f2a0000070008000000000000c0c);

    Passkey adminA;
    Passkey adminB;
    Passkey operatorA;

    uint128 nextAction = 1;

    event ActionApplied(
        bytes16 indexed actionId,
        bytes32 indexed keyId,
        bytes16 indexed orgId,
        SignedAction.Type actionType,
        bytes32 detailsHash,
        uint256 itemCount
    );
    event ItemMoved(uint256 indexed itemId, bytes16 indexed actionId, SignedMappingWMS.Status status);

    function setUp() public {
        registry = new ConsortiumRegistry();
        wms = new SignedMappingWMS(registry);
        adminA = makePasskey(0xA11CE);
        adminB = makePasskey(0xB0B);
        operatorA = makePasskey(0x0FE7A);
        registry.registerOrganization(ORG_A, "Org A", adminA.qx, adminA.qy);
        registry.registerOrganization(ORG_B, "Org B", adminB.qx, adminB.qy);
        _applyKey(_keyAction(adminA, SignedAction.Type.KeyRegister, operatorA));
    }

    // ---------- Registry ----------

    function test_NonOwnerCannotRegisterOrganization() public {
        vm.prank(address(0xBAD));
        vm.expectRevert(ConsortiumRegistry.NotOwner.selector);
        registry.registerOrganization(bytes16(uint128(3)), "x", adminA.qx, adminA.qy);
    }

    function test_AdminRegistersOperatorKeyInOwnOrganization() public view {
        ConsortiumRegistry.Key memory key = registry.getKey(operatorA.keyId);
        assertEq(key.orgId, ORG_A);
        assertTrue(key.role == ConsortiumRegistry.Role.Operator);
        assertTrue(key.active);
    }

    function test_OperatorCannotRegisterKeys() public {
        Passkey memory other = makePasskey(0xC0FFEE);
        ConsortiumRegistry.KeyAction memory a = _keyAction(operatorA, SignedAction.Type.KeyRegister, other);
        _applyKey(a);
        assertTrue(registry.actionState(a.actionId) == SignedAction.State.Rejected);
        assertEq(uint8(registry.getKey(other.keyId).role), 0);
    }

    function test_AdminOfOtherOrganizationCannotRevoke() public {
        ConsortiumRegistry.KeyAction memory a = _keyAction(adminB, SignedAction.Type.KeyRevoke, operatorA);
        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.ActionRejected(a.actionId, SignedAction.RejectReason.WrongOrganization);
        _applyKey(a);
        assertTrue(registry.getKey(operatorA.keyId).active);
    }

    function test_KeyActionSignedByOtherKeyRejectedWithoutConsumingActionId() public {
        Passkey memory other = makePasskey(0xC0FFEE);
        ConsortiumRegistry.KeyAction memory a = _keyAction(adminA, SignedAction.Type.KeyRegister, other);
        a.signerKeyId = adminB.keyId;
        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.ActionRejected(a.actionId, SignedAction.RejectReason.BadSignature);
        _applyKey(a);
        assertTrue(registry.actionState(a.actionId) == SignedAction.State.None);
    }

    function test_KeyActionReplayIsNoop() public {
        Passkey memory other = makePasskey(0xC0FFEE);
        ConsortiumRegistry.KeyAction memory reg = _keyAction(adminA, SignedAction.Type.KeyRegister, other);
        _applyKey(reg);
        _applyKey(_keyAction(adminA, SignedAction.Type.KeyRevoke, other));
        _applyKey(reg);
        assertFalse(registry.getKey(other.keyId).active);
    }

    function test_KeyRegisteredIntoSignerOrganization() public {
        Passkey memory k = makePasskey(0xC0FFEE);
        _applyKey(_keyAction(adminB, SignedAction.Type.KeyRegister, k));
        ConsortiumRegistry.Key memory key = registry.getKey(k.keyId);
        assertEq(key.orgId, ORG_B);
        assertTrue(key.role == ConsortiumRegistry.Role.Operator);
        assertTrue(key.active);
    }

    function test_RegisterExistingKeyRejectedWithKeyExists() public {
        ConsortiumRegistry.KeyAction memory a = _keyAction(adminA, SignedAction.Type.KeyRegister, operatorA);
        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.ActionRejected(a.actionId, SignedAction.RejectReason.KeyExists);
        _applyKey(a);
        assertTrue(registry.actionState(a.actionId) == SignedAction.State.Rejected);

        ConsortiumRegistry.KeyAction memory b = _keyAction(adminB, SignedAction.Type.KeyRegister, operatorA);
        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.ActionRejected(b.actionId, SignedAction.RejectReason.KeyExists);
        _applyKey(b);
        assertEq(registry.getKey(operatorA.keyId).orgId, ORG_A);
    }

    function test_RegisterOffCurveKeyRejectedWithInvalidPublicKey() public {
        Passkey memory bad =
            Passkey(0, bytes32(uint256(1)), bytes32(uint256(1)), keccak256(abi.encode(uint256(1), uint256(1))));
        ConsortiumRegistry.KeyAction memory a = _keyAction(adminA, SignedAction.Type.KeyRegister, bad);
        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.ActionRejected(a.actionId, SignedAction.RejectReason.InvalidPublicKey);
        _applyKey(a);
        assertEq(uint8(registry.getKey(bad.keyId).role), 0);
        assertTrue(registry.actionState(a.actionId) == SignedAction.State.Rejected);
    }

    function test_AdminCannotRevokeAdminKey() public {
        Passkey memory admin2 = makePasskey(0xAD2);
        registry.registerAdminKey(ORG_A, admin2.qx, admin2.qy);
        ConsortiumRegistry.KeyAction memory a = _keyAction(adminA, SignedAction.Type.KeyRevoke, admin2);
        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.ActionRejected(a.actionId, SignedAction.RejectReason.TargetNotOperator);
        _applyKey(a);
        assertTrue(registry.getKey(admin2.keyId).active);
    }

    function test_OwnerCannotRegisterSameOrganizationTwice() public {
        vm.expectRevert(abi.encodeWithSelector(ConsortiumRegistry.OrganizationExists.selector, ORG_A));
        registry.registerOrganization(ORG_A, "Org A again", adminB.qx, adminB.qy);
    }

    function test_OwnerCannotAddAdminKeyToUnknownOrganization() public {
        Passkey memory k = makePasskey(0xC0FFEE);
        vm.expectRevert(abi.encodeWithSelector(ConsortiumRegistry.UnknownOrganization.selector, ORG_C));
        registry.registerAdminKey(ORG_C, k.qx, k.qy);
    }

    function test_OwnerCannotRegisterExistingKey() public {
        vm.expectRevert(abi.encodeWithSelector(ConsortiumRegistry.KeyAlreadyRegistered.selector, operatorA.keyId));
        registry.registerAdminKey(ORG_B, operatorA.qx, operatorA.qy);

        vm.expectRevert(abi.encodeWithSelector(ConsortiumRegistry.KeyAlreadyRegistered.selector, adminA.keyId));
        registry.registerOrganization(ORG_C, "Org C", adminA.qx, adminA.qy);
        assertFalse(registry.organizationExists(ORG_C));
    }

    function test_OwnerCannotRegisterOffCurveKey() public {
        vm.expectRevert(ConsortiumRegistry.InvalidPublicKey.selector);
        registry.registerAdminKey(ORG_A, bytes32(uint256(1)), bytes32(uint256(1)));
    }

    function test_NonOwnerCannotUseOwnerFunctions() public {
        Passkey memory k = makePasskey(0xC0FFEE);
        vm.prank(address(0xBAD));
        vm.expectRevert(ConsortiumRegistry.NotOwner.selector);
        registry.registerAdminKey(ORG_A, k.qx, k.qy);

        vm.prank(address(0xBAD));
        vm.expectRevert(ConsortiumRegistry.NotOwner.selector);
        registry.revokeKey(operatorA.keyId);
    }

    function test_OwnerEmergencyRevokeDeactivatesAnyKey() public {
        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.KeyRevoked(adminA.keyId, ORG_A, bytes16(0));
        registry.revokeKey(adminA.keyId);
        assertFalse(registry.getKey(adminA.keyId).active);

        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.KeyRevoked(operatorA.keyId, ORG_A, bytes16(0));
        registry.revokeKey(operatorA.keyId);
        assertFalse(registry.getKey(operatorA.keyId).active);

        vm.recordLogs();
        registry.revokeKey(operatorA.keyId);
        assertEq(vm.getRecordedLogs().length, 0);

        Passkey memory k = makePasskey(0xDEAD);
        vm.expectRevert(abi.encodeWithSelector(ConsortiumRegistry.UnknownKey.selector, k.keyId));
        registry.revokeKey(k.keyId);
    }

    function test_RevokedKeyIsInactiveAndCannotBeReRegistered() public {
        Passkey memory k = makePasskey(0xC0FFEE);
        ConsortiumRegistry.KeyAction memory signed = _keyAction(operatorA, SignedAction.Type.KeyRegister, k);
        _applyKey(_keyAction(adminA, SignedAction.Type.KeyRevoke, operatorA));
        assertFalse(registry.getKey(operatorA.keyId).active);

        bytes32 ch = SignedAction.challenge(
            signed.actionId, signed.actionType, SignedAction.NO_ITEMS, keccak256(abi.encode(signed.qx, signed.qy))
        );
        (,, SignedAction.RejectReason reason) = registry.verifySigner(operatorA.keyId, ch, signed.auth);
        assertTrue(reason == SignedAction.RejectReason.KeyRevoked);

        ConsortiumRegistry.KeyAction memory again = _keyAction(adminA, SignedAction.Type.KeyRegister, operatorA);
        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.ActionRejected(again.actionId, SignedAction.RejectReason.KeyExists);
        _applyKey(again);

        vm.expectRevert(abi.encodeWithSelector(ConsortiumRegistry.KeyAlreadyRegistered.selector, operatorA.keyId));
        registry.registerAdminKey(ORG_A, operatorA.qx, operatorA.qy);
    }

    function test_RevokedAdminCannotSignKeyActions() public {
        registry.revokeKey(adminA.keyId);
        ConsortiumRegistry.KeyAction memory a = _keyAction(adminA, SignedAction.Type.KeyRegister, makePasskey(0xC0FFEE));
        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.ActionRejected(a.actionId, SignedAction.RejectReason.KeyRevoked);
        _applyKey(a);
        assertTrue(registry.actionState(a.actionId) == SignedAction.State.None);
    }

    function test_OrganizationRegistrationEmitsEvents() public {
        Passkey memory k = makePasskey(0xC1);
        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.OrganizationRegistered(ORG_C, "Org C");
        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.KeyRegistered(k.keyId, ORG_C, ConsortiumRegistry.Role.Admin, k.qx, k.qy, bytes16(0));
        registry.registerOrganization(ORG_C, "Org C", k.qx, k.qy);
    }

    function test_KeyActionEventsCarryActionId() public {
        Passkey memory x = makePasskey(0xC2);
        ConsortiumRegistry.KeyAction memory reg = _keyAction(adminA, SignedAction.Type.KeyRegister, x);
        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.KeyRegistered(
            x.keyId, ORG_A, ConsortiumRegistry.Role.Operator, x.qx, x.qy, reg.actionId
        );
        _applyKey(reg);

        ConsortiumRegistry.KeyAction memory rev = _keyAction(adminA, SignedAction.Type.KeyRevoke, x);
        vm.expectEmit(address(registry));
        emit ConsortiumRegistry.KeyRevoked(x.keyId, ORG_A, rev.actionId);
        _applyKey(rev);
    }

    function test_KeyActionReplayEmitsNothing() public {
        Passkey memory x = makePasskey(0xC3);
        ConsortiumRegistry.KeyAction memory reg = _keyAction(adminA, SignedAction.Type.KeyRegister, x);
        _applyKey(reg);
        vm.recordLogs();
        _applyKey(reg);
        assertEq(vm.getRecordedLogs().length, 0);
        assertTrue(registry.getKey(x.keyId).active);
    }

    // ---------- FSM ----------

    function test_ReceiveMovesEveryItemAndRecordsSigner() public {
        uint256[] memory ids = _ids(3, 100);
        SignedMappingWMS.Action memory a = _action(operatorA, SignedAction.Type.Receive, ids, keccak256("details"));
        for (uint256 i; i < ids.length; ++i) {
            vm.expectEmit(address(wms));
            emit ItemMoved(ids[i], a.actionId, SignedMappingWMS.Status.Accepted);
        }
        vm.expectEmit(address(wms));
        emit ActionApplied(a.actionId, operatorA.keyId, ORG_A, SignedAction.Type.Receive, keccak256("details"), 3);
        _apply(a);
        for (uint256 i; i < ids.length; ++i) {
            assertTrue(wms.itemStatus(ids[i]) == SignedMappingWMS.Status.Accepted);
        }
    }

    function test_FullLifecycle() public {
        uint256[] memory ids = _ids(2, 200);
        _apply(_action(operatorA, SignedAction.Type.Receive, ids, bytes32(0)));
        _apply(_action(operatorA, SignedAction.Type.PutAway, ids, bytes32(0)));
        _apply(_action(adminA, SignedAction.Type.Pick, ids, bytes32(0)));
        _apply(_action(operatorA, SignedAction.Type.Ship, ids, bytes32(0)));
        assertTrue(wms.itemStatus(ids[0]) == SignedMappingWMS.Status.Shipped);
        assertTrue(wms.itemStatus(ids[1]) == SignedMappingWMS.Status.Shipped);
    }

    function test_SignatureOverOtherItemsRejected() public {
        SignedMappingWMS.Action memory a = _action(operatorA, SignedAction.Type.Receive, _ids(3, 300), bytes32(0));
        a.itemIds[2] += 1;
        _expectRejected(a.actionId, SignedAction.RejectReason.BadSignature, 0);
        _apply(a);
        assertTrue(wms.actionState(a.actionId) == SignedAction.State.None);
        assertTrue(wms.itemStatus(a.itemIds[0]) == SignedMappingWMS.Status.None);
    }

    function test_SignatureOverOtherDetailsRejected() public {
        SignedMappingWMS.Action memory a =
            _action(operatorA, SignedAction.Type.Receive, _ids(1, 400), keccak256("bin-1"));
        a.detailsHash = keccak256("bin-2");
        _expectRejected(a.actionId, SignedAction.RejectReason.BadSignature, 0);
        _apply(a);
    }

    function test_ActionIsAtomicAndSiblingsStillApply() public {
        uint256[] memory first = _ids(3, 500);
        _apply(_action(operatorA, SignedAction.Type.Receive, _slice(first, 1, 2), bytes32(0)));

        SignedMappingWMS.Action[] memory batch = new SignedMappingWMS.Action[](2);
        batch[0] = _action(operatorA, SignedAction.Type.Receive, first, bytes32(0));
        batch[1] = _action(operatorA, SignedAction.Type.Receive, _ids(2, 600), bytes32(0));
        _expectRejected(batch[0].actionId, SignedAction.RejectReason.WrongItemStatus, 1);
        wms.applyActions(batch);

        assertTrue(wms.actionState(batch[0].actionId) == SignedAction.State.Rejected);
        assertTrue(wms.itemStatus(first[0]) == SignedMappingWMS.Status.None);
        assertTrue(wms.itemStatus(first[2]) == SignedMappingWMS.Status.None);
        assertTrue(wms.actionState(batch[1].actionId) == SignedAction.State.Applied);
    }

    function test_DuplicateActionIsSilentlySkipped() public {
        uint256[] memory ids = _ids(2, 700);
        SignedMappingWMS.Action memory a = _action(operatorA, SignedAction.Type.Receive, ids, bytes32(0));
        _apply(a);
        vm.recordLogs();
        _apply(a);
        assertEq(vm.getRecordedLogs().length, 0);
        assertTrue(wms.itemStatus(ids[0]) == SignedMappingWMS.Status.Accepted);
    }

    function test_UnsortedOrRepeatedItemsRejected() public {
        uint256[] memory ids = _ids(3, 800);
        ids[2] = ids[1];
        SignedMappingWMS.Action memory a = _action(operatorA, SignedAction.Type.Receive, ids, bytes32(0));
        _expectRejected(a.actionId, SignedAction.RejectReason.ItemsNotAscending, 2);
        _apply(a);
    }

    function test_KeyActionTypeRejectedByWms() public {
        SignedMappingWMS.Action memory a = _action(operatorA, SignedAction.Type.KeyRegister, _ids(1, 900), bytes32(0));
        _expectRejected(a.actionId, SignedAction.RejectReason.WrongType, 0);
        _apply(a);
    }

    function test_UnknownKeyRejected() public {
        SignedMappingWMS.Action memory a =
            _action(makePasskey(0xDEAD), SignedAction.Type.Receive, _ids(1, 1000), bytes32(0));
        _expectRejected(a.actionId, SignedAction.RejectReason.UnknownKey, 0);
        _apply(a);
    }

    function test_ActionSignedBeforeRevocationButArrivingAfterIsRejected() public {
        SignedMappingWMS.Action memory a = _action(operatorA, SignedAction.Type.Receive, _ids(1, 1100), bytes32(0));
        _applyKey(_keyAction(adminA, SignedAction.Type.KeyRevoke, operatorA));
        _expectRejected(a.actionId, SignedAction.RejectReason.KeyRevoked, 0);
        _apply(a);
    }

    // ---------- helpers ----------

    function _action(Passkey memory signer, SignedAction.Type t, uint256[] memory ids, bytes32 detailsHash)
        internal
        returns (SignedMappingWMS.Action memory a)
    {
        bytes16 actionId = bytes16(nextAction++);
        bytes32 ch =
            keccak256(abi.encode(SignedAction.TYPEHASH, actionId, t, keccak256(abi.encodePacked(ids)), detailsHash));
        a = SignedMappingWMS.Action(actionId, t, ids, detailsHash, signer.keyId, signChallenge(signer, ch));
    }

    function _keyAction(Passkey memory signer, SignedAction.Type t, Passkey memory subject)
        internal
        returns (ConsortiumRegistry.KeyAction memory a)
    {
        bytes16 actionId = bytes16(nextAction++);
        bytes32 ch = keccak256(
            abi.encode(
                SignedAction.TYPEHASH, actionId, t, SignedAction.NO_ITEMS, keccak256(abi.encode(subject.qx, subject.qy))
            )
        );
        a = ConsortiumRegistry.KeyAction(actionId, t, subject.qx, subject.qy, signer.keyId, signChallenge(signer, ch));
    }

    function _apply(SignedMappingWMS.Action memory a) internal {
        SignedMappingWMS.Action[] memory batch = new SignedMappingWMS.Action[](1);
        batch[0] = a;
        wms.applyActions(batch);
    }

    function _applyKey(ConsortiumRegistry.KeyAction memory a) internal {
        ConsortiumRegistry.KeyAction[] memory batch = new ConsortiumRegistry.KeyAction[](1);
        batch[0] = a;
        registry.applyKeyActions(batch);
    }

    function _expectRejected(bytes16 actionId, SignedAction.RejectReason reason, uint256 itemIndex) internal {
        vm.expectEmit(address(wms));
        emit SignedMappingWMS.ActionRejected(actionId, reason, itemIndex);
    }

    function _ids(uint256 n, uint256 seed) internal pure returns (uint256[] memory ids) {
        ids = new uint256[](n);
        for (uint256 i; i < n; ++i) {
            ids[i] = (seed << 128) + i;
        }
    }

    function _slice(uint256[] memory ids, uint256 from, uint256 to) internal pure returns (uint256[] memory out) {
        out = new uint256[](to - from);
        for (uint256 i; i < out.length; ++i) {
            out[i] = ids[from + i];
        }
    }
}
