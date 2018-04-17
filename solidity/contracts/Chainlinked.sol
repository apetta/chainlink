pragma solidity ^0.4.18;

import "./BytesUtils.sol";
import "./ChainlinkLib.sol";
import "./LinkToken.sol";
import "./Oracle.sol";
import "./Buffer.sol";
import "./CBOR.sol";


contract Chainlinked is BytesUtils {
  using ChainlinkLib for ChainlinkLib.Run;
  using CBOR for Buffer.buffer;

  uint256 constant clArgsVersion = 1;

  LinkToken internal link;
  Oracle internal oracle;
  uint256 internal requests = 1;

  function newRun(
    bytes32 _jobId,
    address _callbackAddress,
    string _callbackFunctionSignature
  ) internal returns (ChainlinkLib.Run memory) {
    ChainlinkLib.Run memory run;
    run.id = keccak256(this, requests++);
    run.jobId = _jobId;
    run.callbackAddress = _callbackAddress;
    run.callbackFunctionId = bytes4(keccak256(_callbackFunctionSignature));

    // both keccak lines take up only 8k gas

    Buffer.init(run.buf, 64);
    //CBOR.startMap(run.buf);
    run.buf.startMap();

    return run;
  }

  function chainlinkRequest(ChainlinkLib.Run memory _run)
    internal
    returns(bytes32)
  {
    bytes4 fid = bytes4(keccak256("requestData(uint256,bytes32,address,bytes4,bytes32,bytes)"));

    bytes memory payload = append(append(append(append(append(append(append(
      bytes4toBytes(fid),
      uint256toBytes(clArgsVersion)),
      bytes32toBytes(_run.jobId)),
      addressToBytes(_run.callbackAddress)),
      bytes4toBytes(_run.callbackFunctionId)),
      bytes32toBytes(_run.id)),
      uint256toBytes(192)),
      _run.close());

    link.transferAndCall(oracle, 0, payload);

    return _run.id;
  }

  function setOracle(address _oracle) internal {
    oracle = Oracle(_oracle);
  }

  function setLinkToken(address _link) internal {
    link = LinkToken(_link);
  }

  modifier onlyOracle() {
    require(msg.sender == address(oracle));
    _;
  }
}
