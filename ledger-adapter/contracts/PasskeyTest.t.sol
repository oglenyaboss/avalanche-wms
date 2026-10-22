// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

import {Test} from "forge-std/Test.sol";
import {Base64} from "@openzeppelin/contracts/utils/Base64.sol";
import {WebAuthn} from "@openzeppelin/contracts/utils/cryptography/WebAuthn.sol";

abstract contract PasskeyTest is Test {
    uint256 internal constant P256_N = 0xFFFFFFFF00000000FFFFFFFFFFFFFFFFBCE6FAADA7179E84F3B9CAC2FC632551;
    string internal constant ORIGIN = "http://localhost:5173";
    string internal constant RP_ID = "localhost";

    struct Passkey {
        uint256 privateKey;
        bytes32 qx;
        bytes32 qy;
        bytes32 keyId;
    }

    function makePasskey(uint256 privateKey) internal pure returns (Passkey memory p) {
        (uint256 x, uint256 y) = vm.publicKeyP256(privateKey);
        p = Passkey(privateKey, bytes32(x), bytes32(y), keccak256(abi.encode(x, y)));
    }

    function signChallenge(Passkey memory p, bytes32 challenge) internal pure returns (WebAuthn.WebAuthnAuth memory) {
        string memory clientDataJSON = string.concat(
            '{"type":"webauthn.get","challenge":"',
            Base64.encodeURL(abi.encodePacked(challenge)),
            '","origin":"',
            ORIGIN,
            '","crossOrigin":false}'
        );
        bytes memory authenticatorData = abi.encodePacked(sha256(bytes(RP_ID)), bytes1(0x05), uint32(1));
        bytes32 digest = sha256(abi.encodePacked(authenticatorData, sha256(bytes(clientDataJSON))));
        (bytes32 r, bytes32 s) = vm.signP256(p.privateKey, digest);
        if (uint256(s) > P256_N / 2) s = bytes32(P256_N - uint256(s));
        return WebAuthn.WebAuthnAuth(r, s, 23, 1, authenticatorData, clientDataJSON);
    }
}
