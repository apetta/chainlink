pragma solidity ^0.4.23;

import "./lib/CBOR.sol";

library ChainlinkLib {
  bytes4 internal constant oracleRequestDataFid = bytes4(keccak256("requestData(uint256,bytes32,address,bytes4,bytes32,bytes)"));
  bytes4 internal constant oracleSpecAndRunFid = bytes4(keccak256("specAndRun(uint256,address,bytes4,bytes32,bytes)"));

  using CBOR for Buffer.buffer;

  struct Run {
    bytes32 specId;
    address callbackAddress;
    bytes4 callbackFunctionId;
    bytes32 requestId;
    Buffer.buffer buf;
  }

  function initialize(
    Run memory self,
    bytes32 _specId,
    address _callbackAddress,
    string _callbackFunctionSignature
  ) internal pure returns (ChainlinkLib.Run memory) {
    Buffer.init(self.buf, 128);
    self.specId = _specId;
    self.callbackAddress = _callbackAddress;
    self.callbackFunctionId = bytes4(keccak256(_callbackFunctionSignature));
    self.buf.startMap();
    return self;
  }

  function encodeForOracle(
    Run memory self,
    uint256 _clArgsVersion
  ) internal pure returns (bytes memory) {
    return abi.encodeWithSelector(
      oracleRequestDataFid,
      _clArgsVersion,
      self.specId,
      self.callbackAddress,
      self.callbackFunctionId,
      self.requestId,
      self.buf.buf);
  }

  function add(Run memory self, string _key, string _value)
    internal pure
  {
    self.buf.encodeString(_key);
    self.buf.encodeString(_value);
  }

  function addInt(Run memory self, string _key, int256 _value)
    internal pure
  {
    self.buf.encodeString(_key);
    self.buf.encodeInt(_value);
  }
  
  function addUint(Run memory self, string _key, uint256 _value)
    internal pure
  {
    self.buf.encodeString(_key);
    self.buf.encodeUInt(_value);
  }

  function addStringArray(Run memory self, string _key, string[] memory _values)
    internal pure
  {
    self.buf.encodeString(_key);
    self.buf.startArray();
    for (uint256 i = 0; i < _values.length; i++) {
      self.buf.encodeString(_values[i]);
    }
    self.buf.endSequence();
  }

  function close(Run memory self) internal pure {
    self.buf.endSequence();
  }

  struct Spec {
    address callbackAddress;
    bytes4 callbackFunctionId;
    bytes32 requestId;
    Buffer.buffer buf;
  }

  function initialize(
    Spec memory self,
    string[] _tasks,
    address _callbackAddress,
    string _callbackFunctionSignature
  ) internal pure returns (ChainlinkLib.Spec memory) {
    Buffer.init(self.buf, 128);
    self.callbackAddress = _callbackAddress;
    self.callbackFunctionId = bytes4(keccak256(_callbackFunctionSignature));

    self.buf.startMap();
    self.buf.encodeString("tasks");
    self.buf.startArray();
    for (uint256 i = 0; i < _tasks.length; i++) {
      self.buf.encodeString(_tasks[i]);
    }
    self.buf.endSequence();
    self.buf.encodeString("params");
    self.buf.startMap();
    return self;
  }

  function encodeForOracle(
    Spec memory self,
    uint256 _clArgsVersion
  ) internal pure returns (bytes memory) {
    return abi.encodeWithSelector(
      oracleSpecAndRunFid,
      _clArgsVersion,
      self.callbackAddress,
      self.callbackFunctionId,
      self.requestId,
      self.buf.buf);
  }

  function add(Spec memory self, string _key, string _value)
    internal pure
  {
    self.buf.encodeString(_key);
    self.buf.encodeString(_value);
  }

  function addInt(Spec memory self, string _key, int256 _value)
    internal pure
  {
    self.buf.encodeString(_key);
    self.buf.encodeInt(_value);
  }
  
  function addUint(Spec memory self, string _key, uint256 _value)
    internal pure
  {
    self.buf.encodeString(_key);
    self.buf.encodeUInt(_value);
  }

  function addStringArray(Spec memory self, string _key, string[] memory _values)
    internal pure
  {
    self.buf.encodeString(_key);
    self.buf.startArray();
    for (uint256 i = 0; i < _values.length; i++) {
      self.buf.encodeString(_values[i]);
    }
    self.buf.endSequence();
  }

  function close(Spec memory self) internal pure {
    self.buf.endSequence();
    self.buf.endSequence();
  }
}
