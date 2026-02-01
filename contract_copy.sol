pragma solidity ^0.8.0; // Specifies the Solidity compiler version

contract HelloWorld {
    // State variable to store the greeting message
    string public message = "Hello World!";
    int public k = 1;

    // Function to retrieve the current message
    function getMessage() public view returns (string memory) {
        return message;
    }

    // Function to set a new message
    function setMessage(string memory _newMessage) public {
        message = _newMessage;
    }
    function addMessage(string memory _addition) public {
        message = string(abi.encodePacked(message,_addition));
    }
}