// SPDX-License-Identifier: MIT
pragma solidity ^0.8.0;

import "forge-std/Test.sol";
import "./BatchMappingWMS.sol";

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

    event ItemTransitionFailed(
        uint256 indexed eventId,
        uint256 indexed itemId,
        BatchMappingWMS.Status actualStatus,
        BatchMappingWMS.Status expectedStatus
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

    // #44: дубликат eventId на per-item функции — no-op (early return), НЕ revert.
    // Kafka at-least-once гарантирует повторы; revert ломал бы DB↔chain consistency.
    function test_duplicateEventId_single_isNoop() public {
        wms.accept(1, 10);

        wms.accept(1, 20); // тот же eventId, другой item — повтор, игнорируется
        assertEq(uint256(wms.itemStatus(10)), uint256(BatchMappingWMS.Status.Accepted));
        assertEq(uint256(wms.itemStatus(20)), uint256(BatchMappingWMS.Status.None));
    }

    // #44/N9: дубликат eventId ВНУТРИ одного batch — второй элемент скипается,
    // batch не ревертится, валидный первый элемент обрабатывается.
    function test_duplicateEventId_withinBatch_skipsSecond() public {
        uint256[] memory eIds = new uint256[](2);
        uint256[] memory iIds = new uint256[](2);
        eIds[0] = 1; eIds[1] = 1; // дубликат внутри одного batch
        iIds[0] = 10; iIds[1] = 20;

        wms.batchAccept(eIds, iIds);
        assertEq(uint256(wms.itemStatus(10)), uint256(BatchMappingWMS.Status.Accepted));
        assertEq(uint256(wms.itemStatus(20)), uint256(BatchMappingWMS.Status.None));
    }

    // #44/S2: дубликат eventId между вызовами (crash-recovery redelivery) — скип, не revert.
    function test_duplicateEventId_acrossCalls_skips() public {
        wms.accept(1, 10);

        uint256[] memory eIds = new uint256[](1);
        uint256[] memory iIds = new uint256[](1);
        eIds[0] = 1; iIds[0] = 20;

        wms.batchAccept(eIds, iIds);
        assertEq(uint256(wms.itemStatus(20)), uint256(BatchMappingWMS.Status.None));
    }

    // #44: повторная отправка того же eventId+item не делает двойной transition (идемпотентность).
    function test_duplicateEventId_noDoubleTransition() public {
        wms.accept(5, 50);
        assertEq(uint256(wms.itemStatus(50)), uint256(BatchMappingWMS.Status.Accepted));

        wms.accept(5, 50); // точный повтор — игнорируется, статус не меняется
        assertEq(uint256(wms.itemStatus(50)), uint256(BatchMappingWMS.Status.Accepted));
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


    // #47/S3: один невалидный элемент batch'а НЕ ревертит всю транзакцию.
    // Валидные siblings обрабатываются, плохой скипается с ItemTransitionFailed.
    function test_batchWithOneInvalidItem_processesValidSiblings() public {
        // Accept items 100, 200
        uint256[] memory eA = new uint256[](2);
        uint256[] memory iA = new uint256[](2);
        eA[0] = 1; eA[1] = 2;
        iA[0] = 100; iA[1] = 200;
        wms.batchAccept(eA, iA);

        // PutAway batch: item 100 (valid), item 300 (never accepted → poison)
        uint256[] memory eP = new uint256[](2);
        uint256[] memory iP = new uint256[](2);
        eP[0] = 3; eP[1] = 4;
        iP[0] = 100; iP[1] = 300;

        wms.batchPutAway(eP, iP);

        // 100 продвинулся, 300 остался None — НЕ откатил весь batch.
        assertEq(uint256(wms.itemStatus(100)), uint256(BatchMappingWMS.Status.PutAway));
        assertEq(uint256(wms.itemStatus(300)), uint256(BatchMappingWMS.Status.None));
    }

    // #47/S3: плохой элемент эмитит ItemTransitionFailed (actual=None, expected=Accepted),
    // валидный — обычный ItemTransition. Видимость пропуска для chain-status gate (#45).
    function test_batchPoisoning_emitsItemTransitionFailed() public {
        uint256[] memory eA = new uint256[](1);
        uint256[] memory iA = new uint256[](1);
        eA[0] = 1; iA[0] = 100;
        wms.batchAccept(eA, iA);

        uint256[] memory eP = new uint256[](2);
        uint256[] memory iP = new uint256[](2);
        eP[0] = 3; eP[1] = 4;
        iP[0] = 100; iP[1] = 300;

        // Плохой элемент (event 4, item 300): None != Accepted.
        vm.expectEmit(true, true, false, true);
        emit ItemTransitionFailed(
            4, 300,
            BatchMappingWMS.Status.None,
            BatchMappingWMS.Status.Accepted
        );
        wms.batchPutAway(eP, iP);
    }

    // #47: после skip'а eventId плохого элемента помечен processed (consumed, не ретраится).
    function test_batchPoisoning_marksPoisonEventProcessed() public {
        uint256[] memory eP = new uint256[](1);
        uint256[] memory iP = new uint256[](1);
        eP[0] = 7; iP[0] = 700; // 700 never accepted → poison

        wms.batchPutAway(eP, iP);
        assertTrue(wms.processedEventIds(7));
        assertEq(uint256(wms.itemStatus(700)), uint256(BatchMappingWMS.Status.None));
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
