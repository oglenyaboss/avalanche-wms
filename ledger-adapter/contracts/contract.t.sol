// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

import "forge-std/Test.sol";
import "./contract.sol";

contract BatchMappingWMSTest is Test {
    BatchMappingWMS internal wms;
    address internal actor = address(0xBEEF);

    event ItemTransition(
        uint256 indexed eventId,
        uint256 indexed itemId,
        BatchMappingWMS.Status previousStatus,
        BatchMappingWMS.Status nextStatus,
        address actor,
        uint256 timestamp
    );

    function setUp() public {
        wms = new BatchMappingWMS();
        vm.startPrank(actor);
    }

    function test_singleFullCycle() public {
        uint256 item = 42;

        wms.accept(1, item);
        assertEq(uint256(wms.itemStatus(item)), uint256(BatchMappingWMS.Status.Accepted));

        wms.putAway(2, item);
        assertEq(uint256(wms.itemStatus(item)), uint256(BatchMappingWMS.Status.PutAway));

        wms.pick(3, item);
        assertEq(uint256(wms.itemStatus(item)), uint256(BatchMappingWMS.Status.Picked));

        wms.ship(4, item);
        assertEq(uint256(wms.itemStatus(item)), uint256(BatchMappingWMS.Status.Shipped));
    }


    function test_batchFullCycle() public {
        uint256[] memory eIds = new uint256[](3);
        uint256[] memory iIds = new uint256[](3);
        iIds[0] = 100; iIds[1] = 200; iIds[2] = 300;

        // Accept
        eIds[0] = 10; eIds[1] = 11; eIds[2] = 12;
        wms.batchAccept(eIds, iIds);
        for (uint256 i; i < 3; i++) {
            assertEq(uint256(wms.itemStatus(iIds[i])), uint256(BatchMappingWMS.Status.Accepted));
        }

        // PutAway
        eIds[0] = 20; eIds[1] = 21; eIds[2] = 22;
        wms.batchPutAway(eIds, iIds);
        for (uint256 i; i < 3; i++) {
            assertEq(uint256(wms.itemStatus(iIds[i])), uint256(BatchMappingWMS.Status.PutAway));
        }

        // Pick
        eIds[0] = 30; eIds[1] = 31; eIds[2] = 32;
        wms.batchPick(eIds, iIds);
        for (uint256 i; i < 3; i++) {
            assertEq(uint256(wms.itemStatus(iIds[i])), uint256(BatchMappingWMS.Status.Picked));
        }

        // Ship
        eIds[0] = 40; eIds[1] = 41; eIds[2] = 42;
        wms.batchShip(eIds, iIds);
        for (uint256 i; i < 3; i++) {
            assertEq(uint256(wms.itemStatus(iIds[i])), uint256(BatchMappingWMS.Status.Shipped));
        }
    }

    function test_emitsItemTransition() public {
        vm.expectEmit(true, true, false, true);
        emit ItemTransition(
            1, 42,
            BatchMappingWMS.Status.None,
            BatchMappingWMS.Status.Accepted,
            actor,
            block.timestamp
        );
        wms.accept(1, 42);
    }

    function test_revert_duplicateEventId_single() public {
        wms.accept(1, 10);

        vm.expectRevert("Duplicate eventId");
        wms.accept(1, 20); // тот же eventId, другой item
    }

    function test_revert_duplicateEventId_batch() public {
        uint256[] memory eIds = new uint256[](2);
        uint256[] memory iIds = new uint256[](2);
        eIds[0] = 1; eIds[1] = 1; // дубликат внутри одного batch
        iIds[0] = 10; iIds[1] = 20;

        vm.expectRevert("Duplicate eventId");
        wms.batchAccept(eIds, iIds);
    }

    function test_revert_duplicateEventId_acrossCalls() public {
        wms.accept(1, 10);

        uint256[] memory eIds = new uint256[](1);
        uint256[] memory iIds = new uint256[](1);
        eIds[0] = 1; iIds[0] = 20;

        vm.expectRevert("Duplicate eventId");
        wms.batchAccept(eIds, iIds);
    }

    function test_revert_invalidTransition_putAwayWithoutAccept() public {
        vm.expectRevert("Invalid status transition");
        wms.putAway(1, 10);
    }

    function test_revert_invalidTransition_pickWithoutPutAway() public {
        wms.accept(1, 10);

        vm.expectRevert("Invalid status transition");
        wms.pick(2, 10);
    }

    function test_revert_invalidTransition_shipWithoutPick() public {
        wms.accept(1, 10);
        wms.putAway(2, 10);

        vm.expectRevert("Invalid status transition");
        wms.ship(3, 10);
    }

    function test_revert_invalidTransition_doubleAccept() public {
        wms.accept(1, 10);

        vm.expectRevert("Invalid status transition");
        wms.accept(2, 10);
    }


    function test_revert_batchWithOneInvalidItem() public {
        // Accept items 100, 200
        uint256[] memory eA = new uint256[](2);
        uint256[] memory iA = new uint256[](2);
        eA[0] = 1; eA[1] = 2;
        iA[0] = 100; iA[1] = 200;
        wms.batchAccept(eA, iA);

        // PutAway batch: item 100 (valid), item 300 (not accepted → revert)
        uint256[] memory eP = new uint256[](2);
        uint256[] memory iP = new uint256[](2);
        eP[0] = 3; eP[1] = 4;
        iP[0] = 100; iP[1] = 300;

        vm.expectRevert("Invalid status transition");
        wms.batchPutAway(eP, iP);

        // item 100 не должен был перейти — весь batch откатился
        assertEq(uint256(wms.itemStatus(100)), uint256(BatchMappingWMS.Status.Accepted));
    }

    function test_revert_arrayLengthMismatch() public {
        uint256[] memory eIds = new uint256[](2);
        uint256[] memory iIds = new uint256[](1);
        eIds[0] = 1; eIds[1] = 2;
        iIds[0] = 10;

        vm.expectRevert("Array length mismatch");
        wms.batchAccept(eIds, iIds);
    }

    function test_revert_emptyArrays() public {
        uint256[] memory eIds = new uint256[](0);
        uint256[] memory iIds = new uint256[](0);

        vm.expectRevert("Empty arrays");
        wms.batchAccept(eIds, iIds);
    }

    function test_processedEventIdsMarked() public {
        assertFalse(wms.processedEventIds(1));
        wms.accept(1, 10);
        assertTrue(wms.processedEventIds(1));
    }

    function test_differentActors() public {
        vm.stopPrank();

        vm.prank(address(0xA));
        wms.accept(1, 10);

        vm.prank(address(0xB));
        wms.putAway(2, 10);

        assertEq(uint256(wms.itemStatus(10)), uint256(BatchMappingWMS.Status.PutAway));
    }
}
