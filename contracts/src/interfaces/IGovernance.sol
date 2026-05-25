// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

interface IGovernance {
    function propose(address target, bytes calldata data, string calldata description) external returns (uint256 proposalId);
    function vote(uint256 proposalId, bool support) external;
    function execute(uint256 proposalId) external;
    function getProposal(uint256 proposalId) external view returns (
        address target, bytes memory data, string memory description,
        uint256 forVotes, uint256 againstVotes,
        uint256 startTime, uint256 endTime, bool executed
    );
}
