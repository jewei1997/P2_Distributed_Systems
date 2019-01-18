//
// raft.go
// =======
// Write your code in this file
// We will use the original version of all other
// files for testing
//

package raft

import (
	"fmt"
	"github.com/cmu440/rpc"
	"math/rand"
	"os"
	"sync"
	"time"
)

type Mode int

const (
	Follower  Mode = 0
	Candidate Mode = 1
	Leader    Mode = 2
)

const DEBUG = false

//
// API
// ===
// This is an outline of the API that your raft implementation should
// expose.
//
// rf = Make(...)
//   Create a new Raft peer.
//
// rf.Start(command interface{}) (index, term, isleader)
//   Start agreement on a new log entry
//
// rf.GetState() (me, term, isLeader)
//   Ask a Raft peer for "me" (see line 58), its current term, and whether it thinks it
//   is a leader
//
// ApplyMsg
//   Each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (e.g. tester) on the
//   same peer, via the applyCh channel passed to Make()
//

type LogEntry struct {
	Command      interface{} // command for state machine
	TermRecieved int         // term when entry was received by leader
}

//
// ApplyMsg
// ========
//
// As each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same peer, via the applyCh passed to Make()
//
type ApplyMsg struct {
	Index   int
	Command interface{}
}

//
// Raft struct
// ===========
//
// A Go object implementing a single Raft peer
//
type Raft struct {
	mux         sync.Mutex       // Lock to protect shared access to this peer's state
	peers       []*rpc.ClientEnd // RPC end points of all peers
	me          int              // this peer's index into peers[]
	applyCh     chan ApplyMsg    // applyCh is a channel on which the tester or service expects Raft to send ApplyMsg messages
	heartbeatCh chan *AppendEntriesReply

	curMode Mode // indicates whether follower, candidate, or leader
	votesCh chan *RequestVoteReply

	// Your data here (2A, 2B).
	// Look at the Raft paper's Figure 2 for a description of what
	// state a Raft peer should maintain

	// Volatile state on all servers
	currentTerm int         // latest term server has seen
	votedFor    int         // candidateId that recieved vote in current term (or null if none)
	log         []*LogEntry // log entries
	commitIndex int         // index of highest log entry known to be commited
	lastApplied int         // index of highest log entry applied to state machine

	// Volatile State on leaders
	// Reinitialized after election
	nextIndex  []int // for each server, index of the next log entry to send to that server
	matchIndex []int // for each server, index of highest log entry known to be replicated on server
}

//
// GetState()
// ==========
//
// Return "me", current term and whether this peer
// believes it is the leader
//
func (rf *Raft) GetState() (int, int, bool) {
	return GetMe(rf), GetCurTerm(rf), GetCurMode(rf) == Leader
}

//
// RequestVoteArgs
// ===============
//
// Example RequestVote RPC arguments structure
//
// Please note
// ===========
// Field names must start with capital letters!
//
type RequestVoteArgs struct {
	// Arguments
	Term         int // candidate's term
	CandidateId  int // candidate requesting vote
	LastLogIndex int // index of candidate's last log entry
	LastLogTerm  int // term of candidate's last log entry
}

//
// RequestVoteReply
// ================
//
// Example RequestVote RPC reply structure.
//
// Please note
// ===========
// Field names must start with capital letters!
//
//
type RequestVoteReply struct {
	// Results
	Term          int  // currentTerm for candidate to update itself
	CandidateTerm int  // the term the vote is for. this is to prevent previous votes for counting for future votes
	VoteGranted   bool // true means some follower has granted vote to some candidate
}

type AppendEntriesArgs struct {
	Term         int         // leader's term
	LeaderId     int         // so follower can redirect clients
	PrevLogIndex int         // index of log entry immediately precending new ones
	PrevLogTerm  int         // term of PrevLogIndex
	Entries      []*LogEntry // log entries to store (empty for heartbeat; may send more than one for efficiency)
	LeaderCommit int         // leader's commitIndex
}

type AppendEntriesReply struct {
	Term          int  // currentTerm, for leader to update itself
	Success       bool // true if follower contained entry matching prevLogIndex and prevLogTerm
	From          int  // who this reply is from, used to update nextIndex from the follower
	TermIncorrect bool
}

//
// RequestVote
// ===========
//
// Example RequestVote RPC handler
//
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	reply.Term = GetCurTerm(rf)
	reply.CandidateTerm = args.Term

	// if their term is less than our term, we don't vote for them
	if args.Term < GetCurTerm(rf) {
		reply.VoteGranted = false
		return
	}

	// election restriction. If the candidate's log is behind of us,
	// we don't vote for them
	// if our last log index > their log index, dont vote for them
	lastLogIndex := len(GetLog(rf)) - 1
	if args.LastLogIndex < lastLogIndex {
		reply.VoteGranted = false
		return
	}

	// if we have equal logs but our last log's term is greater
	// than their last log's term, we also dont vote for them
	if args.LastLogIndex == lastLogIndex && GetLogEle(rf, lastLogIndex).TermRecieved > args.LastLogTerm {
		reply.VoteGranted = false
		return
	}

	if args.Term == GetCurTerm(rf) {
		// if we are the leader OR
		// we voted for someone else
		if (GetCurMode(rf) == Leader) || (GetVotedFor(rf) != -1 && GetVotedFor(rf) != args.CandidateId) {
			reply.VoteGranted = false
			return
		}
	}

	// if we are outdated
	if args.Term > GetCurTerm(rf) {
		SetVotedFor(rf, -1)
		SetCurTerm(rf, args.Term)
	}

	// votes are considered heartbeats
	rf.heartbeatCh <- &AppendEntriesReply{Term: args.Term}

	SetVotedFor(rf, args.CandidateId)
	reply.VoteGranted = true
	return
}

//
// sendRequestVote
// ===============
//
// Example code to send a RequestVote RPC to a peer
//
// peer int -- index of the target peer in
// rf.peers[]
//
// args *RequestVoteArgs -- RPC arguments in args
//
// reply *RequestVoteReply -- RPC reply
//
// The types of args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers)
//
// The rpc package simulates a lossy network, in which peers
// may be unreachable, and in which requests and replies may be lost
//
// Call() sends a request and waits for a reply
//
// If a reply arrives within a timeout interval, Call() returns true;
// otherwise Call() returns false
//
// Thus Call() may not return for a while
//
// A false return can be caused by a dead peer, a live peer that
// can't be reached, a lost request, or a lost reply
//
// Call() is guaranteed to return (perhaps after a delay)
// *except* if the handler function on the peer side does not return
//
// Thus there
// is no need to implement your own timeouts around Call()
//
// Please look at the comments and documentation in ../rpc/rpc.go
// for more details
//
// If you are having trouble getting RPC to work, check that you have
// capitalized all field names in the struct passed over RPC, and
// that the caller passes the address of the reply struct with "&",
// not the struct itself
//
func (rf *Raft) sendRequestVote(peer int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	for true {
		ok := rf.peers[peer].Call("Raft.RequestVote", args, reply)
		if ok {
			rf.votesCh <- reply
			if DEBUG {
				fmt.Println("peer", peer, "pushed", reply, "into votesCh")
			}
			return true
		}
		if !ok {
			break
		}
	}
	return false
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {

	if DEBUG {
		fmt.Println("Server", GetMe(rf), "received args =", ArgsToString(args))
		fmt.Println("Server", GetMe(rf), "has log = ", LogToString(GetLog(rf)), "last applied = ", GetLastApplied(rf), " commit index = ", GetCommitIndex(rf))
	}

	reply.Term = GetCurTerm(rf)
	reply.From = GetMe(rf)
	reply.TermIncorrect = false

	// if their term is less than our term, we don't vote for them
	if args.Term < GetCurTerm(rf) {
		reply.TermIncorrect = true
		return
	}

	// if args.term >= rf.currentTerm then it counts as a heartbeat!
	// 1) Reply false if term < currentTerm (more for heartbeats)
	rf.heartbeatCh <- reply // reset leader election timeout timer

	// 2) Reply false if log doesnt contain an entry at prevLogIndex
	//    whose term matches with prevLogTerm
	if args.Entries != nil && (args.PrevLogIndex >= len(GetLog(rf)) || GetLogEle(rf, args.PrevLogIndex).TermRecieved != args.PrevLogTerm) {
		reply.Success = false
		return
	}

	// 3) If an existing entry conflicts with a new one (same index
	//    but different terms, delete the existing entry and all that
	//    follow it
	// 4) Append any new entries not already in log
	if args.Entries != nil {
		AddNewEntries(rf, args)
	}

	if DEBUG {
		fmt.Println("Server", GetMe(rf), "log is now = ", LogToString(GetLog(rf)), "commitIndex = ", GetCommitIndex(rf))
	}

	// 5) If leaderCommit > commitIndex, set commitIndex =
	//    min(leaderCommit, index of last new entry)
	if args.LeaderCommit > GetCommitIndex(rf) {
		prevCommitIndex := GetCommitIndex(rf)
		SetCommitIndex(rf, Min(args.LeaderCommit, len(GetLog(rf))-1))
		if DEBUG {
			fmt.Println("Server", GetMe(rf), "updated commit index from", prevCommitIndex, "to", GetCommitIndex(rf))
		}
	}

	reply.Success = true
	if DEBUG {
		fmt.Println("Server", GetMe(rf), "responding with reply =", ReplyToString(reply))
	}
}

// if args.Entries == nil, then we know it is a heartbeat, so
// we will not bother checking reply to see if reply.Successs == true
// But, if args.Entries != nil, then we know it is an append entries
// so if reply.Success == false, we will adjust PrevLogIndex,
// PrevLogTerm, and Entries, and resend the RPC
func (rf *Raft) sendAppendEntries(peer int, replyCh chan *AppendEntriesReply, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	for true {

		// fmt.Println("Server", GetMe(rf), "sent append entries to peer", peer)
		ok := GetPeers(rf)[peer].Call("Raft.AppendEntries", args, reply)

		if !ok {
			break
		}

		// if this is a heartbeat
		if args.Entries == nil {
			rf.heartbeatCh <- reply
			return true
		}

		if reply.TermIncorrect {
			rf.heartbeatCh <- reply
			// replyCh <- reply
			return false
		}

		// otherwise, it is an append entries
		if !reply.Success {
			// decrement nextIndex for this peer, update PLI and PLT,
			// update log and try again
			args.Entries = append([]*LogEntry{GetLogEle(rf, args.PrevLogIndex)}, args.Entries...)

			// nextIndex--
			SetNextIndex(rf, peer, GetNextIndex(rf, peer)-1)

			args.PrevLogIndex = GetNextIndex(rf, peer) - 1
			args.PrevLogTerm = GetLogEle(rf, args.PrevLogIndex).TermRecieved
			continue
		}
		// set matchIndex[peer] = nextIndex[peer] - 1
		SetMatchIndex(rf, peer, GetNextIndex(rf, peer)-1)
		replyCh <- reply
		return true
	}
	return false
}

//
// Start
// =====
//
// The service using Raft (e.g. a k/v peer) wants to start
// agreement on the next command to be appended to Raft's log
//
// If this peer is not the leader, return false
//
// Otherwise start the agreement and return immediately
//
// There is no guarantee that this command will ever be committed to
// the Raft log, since the leader may fail or lose an election
//
// The first return value is the index that the command will appear at
// if it is ever committed
//
// The second return value is the current term
//
// The third return value is true if this peer believes it is
// the leader
//

// NOTE: Start has a lock!!!!!!!!!!
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	index := len(rf.log)
	isLeader := rf.curMode == Leader

	if DEBUG {
		fmt.Println("Start called on Server", rf.me, "with command=", command)
	}

	if isLeader {
		// the first thing we do is append the entry to the log
		entry := []*LogEntry{&LogEntry{Command: command, TermRecieved: rf.currentTerm}}
		AppendToLogWithoutLock(rf, entry)
		go DoAgreement(rf, command)
	}

	return index, rf.currentTerm, isLeader
}

func DoAgreement(rf *Raft, command interface{}) {
	if DEBUG {
		fmt.Println("Server", GetMe(rf), "entered DoAgreement with command", command, " ***********************************, log = ", LogToString(GetLog(rf)), "commitIndex = ", GetCommitIndex(rf))
	}

	if GetCurMode(rf) != Leader {
		errMsg := fmt.Sprintln("Server", GetMe(rf), "in DoAgreement, but is not a leader!")
		ThrowError(errMsg)
	}

	finalCommitIndex := len(GetLog(rf)) - 1

	replyCh := make(chan *AppendEntriesReply)

	for peer := 0; peer < len(GetPeers(rf)); peer++ {

		if peer == GetMe(rf) { // don't want to send RPC to ourselves
			continue
		}

		// build up entries to append
		// should be log[nextIndex:]

		nextIndexPeer := GetNextIndex(rf, peer)

		if nextIndexPeer < 0 || nextIndexPeer >= len(GetLog(rf)) {
			errMsg := fmt.Sprint("Server", GetMe(rf), "Next Index for peer is out of bounds!, next index = ", nextIndexPeer, ", Log = ", LogToString(GetLog(rf)))
			ThrowError(errMsg)
		}

		entriesToSend := GetLog(rf)[nextIndexPeer:]

		appendEntriesArgs := &AppendEntriesArgs{
			Term:         GetCurTerm(rf),
			LeaderId:     GetMe(rf),
			PrevLogIndex: GetNextIndex(rf, peer) - 1,
			PrevLogTerm:  GetLogEle(rf, GetNextIndex(rf, peer)-1).TermRecieved,
			Entries:      entriesToSend,
			LeaderCommit: GetCommitIndex(rf),
		}

		appendEntriesReply := &AppendEntriesReply{}

		if DEBUG {
			fmt.Println("Server", GetMe(rf), "sending appending entries to Server", peer, "with PrevLogIndex = ", appendEntriesArgs.PrevLogIndex)
		}
		go rf.sendAppendEntries(peer, replyCh, appendEntriesArgs, appendEntriesReply)
	}

	// wait around and until you get majority of followers to respond with success
	numSuccesses := 1

	for {
		select {
		case reply := <-replyCh:

			if DEBUG {
				fmt.Println("Server", GetMe(rf), "for command = ", command, " popped reply=", reply, "from appendEntriesCh for command = ", command)
			}

			if GetCurMode(rf) != Leader {
				// errMsg := fmt.Sprint("Server", GetMe(rf), "popped reply=", reply, "from appendEntriesCh for command =", command, "even though it is not a leader! It is a", GetCurMode(rf), "instead!")
				continue
				// ThrowError(errMsg)
			}

			if reply.Success && reply.Term == GetCurTerm(rf) {
				if GetNextIndex(rf, reply.From)+1 <= len(GetLog(rf)) {
					SetNextIndex(rf, reply.From, GetNextIndex(rf, reply.From)+1)
					SetMatchIndex(rf, reply.From, GetNextIndex(rf, reply.From)-1)
					numSuccesses++
				}
			}

			if 2*numSuccesses > len(GetPeers(rf)) {
				if DEBUG {
					fmt.Println("Server", GetMe(rf), "received a majority of success=true from other servers, going to commit command = ", command, " and apply to state machine")
				}
				// increment commitIndex and apply entry to state machine
				if GetCommitIndex(rf)+1 < len(GetLog(rf)) {
					if DEBUG {
						fmt.Println("Server", GetMe(rf), "commit index incremented from", GetCommitIndex(rf), "to", GetCommitIndex(rf)+1)
					}
					// IncrCommitIndex(rf, 1)
					SetCommitIndex(rf, finalCommitIndex)
				}
				if DEBUG {
					fmt.Println("Server", GetMe(rf), "Exiting DoAgreement for command = ", command, " commit index = ", GetCommitIndex(rf), "log is = ", LogToString(GetLog(rf)))
				}
				return
			}
		}
	}
}

//
// Kill
// ====
//
// The tester calls Kill() when a Raft instance will not
// be needed again
//
// You are not required to do anything
// in Kill(), but it might be convenient to (for example)
// turn off debug output from this instance
//
func (rf *Raft) Kill() {
	// Your code here, if desired
}

//
// Make
// ====
//
// The service or tester wants to create a Raft peer
//
// The port numbers of all the Raft peers (including this one)
// are in peers[]
//
// This peer's port is peers[me]
//
// All the peers' peers[] arrays have the same order
//
// applyCh
// =======
//
// applyCh is a channel on which the tester or service expects
// Raft to send ApplyMsg messages
//
// Make() must return quickly, so it should start Goroutines
// for any long-running work
//
func Make(peers []*rpc.ClientEnd, me int, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{
		peers:       peers,
		me:          me,
		applyCh:     applyCh,
		heartbeatCh: make(chan *AppendEntriesReply),
		curMode:     Follower,
		votesCh:     make(chan *RequestVoteReply),

		// Volatile state on all servers
		currentTerm: 0,
		votedFor:    -1,                                       // voted for no one initially
		log:         []*LogEntry{&LogEntry{TermRecieved: -1}}, // log is indexed starting at 1
		commitIndex: 0,
		lastApplied: 0,
	}

	rf.nextIndex = []int{}
	rf.matchIndex = []int{}

	for i := 0; i < len(GetPeers(rf)); i++ {
		rf.nextIndex = append(rf.nextIndex, 0)
		rf.matchIndex = append(rf.matchIndex, 0)
	}

	go FollowerMode(rf)
	return rf
}

func FollowerMode(rf *Raft) {
	SetCurMode(rf, Follower)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	timer := time.NewTimer(time.Duration(r.Intn(300)+200) * time.Millisecond) // election leader timeout [150, 301) ms
	allRulesTimer := time.NewTimer(time.Duration(50) * time.Millisecond)

	for {
		select {
		case <-allRulesTimer.C:
			DoRulesForAllServers(rf)
			allRulesTimer.Reset(time.Duration(50) * time.Millisecond)
		case <-timer.C:
			// timeout - start a leader election
			// if attemping to start leader election is successful, exit
			// otherwise, try again when timeout occurs again
			if DEBUG {
				fmt.Println("Server", GetMe(rf), "timed out, follower -> candidate", "updating cur term from ", GetCurTerm(rf), "to", GetCurTerm(rf)+1)
			}

			IncrCurTerm(rf) // incr current term

			go CandidateMode(rf)
			return // exit FollowerMode

		case reply := <-rf.heartbeatCh:
			if reply.Term > GetCurTerm(rf) {
				SetCurTerm(rf, reply.Term)
			}
			// heartbeat received - reset timer
			timer.Reset(time.Duration(r.Intn(300)+200) * time.Millisecond)
		}
	}
}

func CandidateMode(rf *Raft) {
	SetCurMode(rf, Candidate)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	timer := time.NewTimer(time.Duration(r.Intn(300)+200) * time.Millisecond) // election leader timeout [150, 301) ms
	allRulesTimer := time.NewTimer(time.Duration(50) * time.Millisecond)
	numSuccesses := 1
	// numFailures := 0
	go LeaderElection(rf)
	for {
		select {

		case <-allRulesTimer.C:
			DoRulesForAllServers(rf)
			allRulesTimer.Reset(time.Duration(50) * time.Millisecond)

		case <-timer.C:
			defer CandidateMode(rf)
			return

		case vote := <-rf.votesCh:

			if vote.Term >= GetCurTerm(rf) {
				SetCurTerm(rf, vote.Term)
				if DEBUG {
					fmt.Println("Server", GetMe(rf), "voting for server with higher term, candidate -> follower")
				}
				go FollowerMode(rf)
				return
			}

			if vote.VoteGranted && vote.CandidateTerm == GetCurTerm(rf) {
				numSuccesses++
			}

			if 2*numSuccesses > len(GetPeers(rf)) {
				// leader election was successful, go into leader mode and quit
				if DEBUG {
					fmt.Println("Server", GetMe(rf), "candidate -> leader ....................................")
					fmt.Println("Server", GetMe(rf), "log is = ", LogToString(GetLog(rf)), "commit index = ", GetCommitIndex(rf))
				}
				go LeaderMode(rf)
				return
			}

		case reply := <-rf.heartbeatCh:
			if reply.Term > GetCurTerm(rf) {
				SetCurTerm(rf, reply.Term)
			}
			if DEBUG {
				fmt.Println("Server", GetMe(rf), "candidate -> follower")
			}
			go FollowerMode(rf)
			return
		}
	}
}

func LeaderElection(rf *Raft) {
	SetVotedFor(rf, GetMe(rf))

	requestVoteArgs := &RequestVoteArgs{
		Term:        GetCurTerm(rf),
		CandidateId: GetMe(rf),
		// This is for election restriction
		LastLogIndex: len(GetLog(rf)) - 1,
		LastLogTerm:  GetLogEle(rf, len(GetLog(rf))-1).TermRecieved,
	}

	if DEBUG {
		fmt.Println("Server", GetMe(rf), "sent votes out with args = ", requestVoteArgs)
	}
	for peer := 0; peer < len(GetPeers(rf)); peer++ {
		if peer == GetMe(rf) {
			continue // don't send RPC to yourself
		}

		requestVoteReply := &RequestVoteReply{}

		go rf.sendRequestVote(peer, requestVoteArgs, requestVoteReply)
	}
	return
}

func LeaderMode(rf *Raft) {
	SetCurMode(rf, Leader)
	heartbeatTimer := time.NewTimer(0) // send the first heartbeat immediatley, all other heartbeats every 100 ms
	allRulesTimer := time.NewTimer(time.Duration(50) * time.Millisecond)

	// fill nextIndex arr with len(log)
	for i := 0; i < len(rf.nextIndex); i++ {
		rf.nextIndex[i] = Min(GetCommitIndex(rf)+1, len(GetLog(rf)))
	}

	// fill matchIndex arr with 0's
	for i := 0; i < len(rf.matchIndex); i++ {
		rf.matchIndex[i] = 0
	}

	for {
		select {

		case <-allRulesTimer.C:

			DoRulesForAllServers(rf)

			// If there exists an N such that N > commitIndex, a majority
			// of matchIndex[i] >= N and log[N].term == currentTerm:
			// set commitIndex = N
			maxMatchIndex := 0
			for peer := 0; peer < len(GetPeers(rf)); peer++ {
				maxMatchIndex = Max(maxMatchIndex, GetMatchIndex(rf, peer))
			}

			// check if log[N].term == currentTerm and a majority of
			// matchIndex[i] >= N. If so, commitIndex = N
			for N := GetCommitIndex(rf) + 1; N <= maxMatchIndex; N++ {
				if N >= len(GetLog(rf)) || GetLogEle(rf, N).TermRecieved != GetCurTerm(rf) {
					continue
				}
				count := 0
				for peer := 0; peer < len(GetPeers(rf)); peer++ {
					if GetMatchIndex(rf, peer) >= N {
						count++
					}
				}
				if 2*count > len(GetPeers(rf)) {
					SetCommitIndex(rf, N)
					break
				}
			}

			allRulesTimer.Reset(time.Duration(50) * time.Millisecond)

		case <-heartbeatTimer.C:

			appendEntriesArgs := &AppendEntriesArgs{
				Term:         GetCurTerm(rf),
				LeaderId:     GetMe(rf),
				Entries:      nil, // Entries = nil for heartbeats
				LeaderCommit: GetCommitIndex(rf),
			}

			// send regular heart beats to peers
			for peer := 0; peer < len(GetPeers(rf)); peer++ {
				if peer == GetMe(rf) {
					continue
				}
				appendEntriesReply := &AppendEntriesReply{}

				go rf.sendAppendEntries(peer, nil, appendEntriesArgs, appendEntriesReply)
			}
			heartbeatTimer.Reset(time.Duration(rand.Intn(100)) * time.Millisecond)

		case reply := <-rf.heartbeatCh:
			if reply.Term > GetCurTerm(rf) {
				if DEBUG {
					fmt.Println("Server", GetMe(rf), "leader -> follower")
				}
				SetCurTerm(rf, reply.Term)
				go FollowerMode(rf)
				return
			}
		}
	}
}

func ApplyToStateMachine(rf *Raft, applyMsg ApplyMsg) {
	rf.applyCh <- applyMsg
	return
}

func DoRulesForAllServers(rf *Raft) {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	// 1) If commitIndex > lastApplied: increment lastApplied, apply
	//    log[lastApplied] to state machine

	/*
		fmt.Println("Server", rf.me, "in DoRulesForAllServers, lastApplied =", rf.lastApplied, "log=", LogToString(rf.log), "rf.commitIndex = ", rf.commitIndex)

		for i := rf.lastApplied + 1; i <= rf.commitIndex; i++ {
			applyMsg := ApplyMsg{
				Index:   i,
				Command: rf.log[i].Command,
			}
			fmt.Println("Server", rf.me, "applied ApplyMsg = ", applyMsg, "to state machine")
			go ApplyToStateMachine(rf, applyMsg)
		}

		rf.lastApplied = rf.commitIndex
	*/

	if rf.commitIndex > rf.lastApplied {
		rf.lastApplied++
		if DEBUG {
			fmt.Println("Server", rf.me, "in DoRulesForAllServers, lastApplied =", rf.lastApplied, "log=", LogToString(rf.log), "rf.commitIndex = ", rf.commitIndex)
		}
		applyMsg := ApplyMsg{
			Index:   rf.lastApplied,
			Command: rf.log[rf.lastApplied].Command,
		}
		rf.applyCh <- applyMsg
		if DEBUG {
			fmt.Println("Server", rf.me, "applied ApplyMsg = ", applyMsg, "to state machine")
		}
	}
}

func GetPeers(rf *Raft) []*rpc.ClientEnd {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	return rf.peers
}

func GetVotedFor(rf *Raft) int {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	return rf.votedFor
}

func SetVotedFor(rf *Raft, votedFor int) {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	rf.votedFor = votedFor
}

func GetMe(rf *Raft) int {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	return rf.me
}

func IncrCurTerm(rf *Raft) {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	rf.currentTerm++
}

func GetCurTerm(rf *Raft) int {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	return rf.currentTerm
}

func SetCurTerm(rf *Raft, term int) {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	rf.currentTerm = term
}

func GetLog(rf *Raft) []*LogEntry {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	return rf.log
}

func GetLogEle(rf *Raft, index int) *LogEntry {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	return rf.log[index]
}

func GetCommitIndex(rf *Raft) int {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	return rf.commitIndex
}

func GetCurMode(rf *Raft) Mode {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	return rf.curMode
}

func SetCurMode(rf *Raft, mode Mode) {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	rf.curMode = mode
}

func GetLastApplied(rf *Raft) int {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	return rf.lastApplied
}

func GetNextIndex(rf *Raft, index int) int {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	return rf.nextIndex[index]
}

func SetNextIndex(rf *Raft, index int, nextIndex int) {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	rf.nextIndex[index] = nextIndex
}

func GetMatchIndex(rf *Raft, index int) int {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	return rf.matchIndex[index]
}

func SetMatchIndex(rf *Raft, index int, matchIndex int) {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	rf.matchIndex[index] = matchIndex
}

// appends entries to the log
// does not actually apply anything to the state machine
func AppendToLogWithoutLock(rf *Raft, entries []*LogEntry) {
	for i := 0; i < len(entries); i++ {
		rf.log = append(rf.log, entries[i])
	}
}

func AppendToLogWithLock(rf *Raft, entries []*LogEntry) {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	for i := 0; i < len(entries); i++ {
		rf.log = append(rf.log, entries[i])
	}
}

func AddNewEntries(rf *Raft, args *AppendEntriesArgs) {
	rf.mux.Lock()
	defer rf.mux.Unlock()

	for i := 0; i < len(args.Entries); i++ {

		if args.PrevLogIndex+i+1 == len(rf.log) {
			AppendToLogWithoutLock(rf, args.Entries[i:])
			break
		}

		// conflict
		if rf.log[args.PrevLogIndex+i+1].TermRecieved != args.Entries[i].TermRecieved {
			rf.log = rf.log[:args.PrevLogIndex+i+1]
			// append everything from args.Entries >= index [i] to rf.log
			for j := i; j < len(args.Entries); j++ {
				rf.log = append(rf.log, args.Entries[j])
			}
			break
		}
	}

}

func SetCommitIndex(rf *Raft, commitIndex int) {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	rf.commitIndex = commitIndex
}

func IncrCommitIndex(rf *Raft, delta int) {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	rf.commitIndex += delta
}

func IncrLastApplied(rf *Raft, delta int) {
	rf.mux.Lock()
	defer rf.mux.Unlock()
	rf.lastApplied += delta
}

func ThrowError(str string) {
	fmt.Println(str)
	os.Exit(1)
}

func Min(x int, y int) int {
	if x < y {
		return x
	}
	return y
}

func Max(x int, y int) int {
	if x < y {
		return y
	}
	return x
}

// Note: this function does not acquire the rf mux lock, so it should
// only be used in functions that DO acquire it!
func LogToString(log []*LogEntry) string {
	str := "["
	for i := 0; i < len(log); i++ {
		str += fmt.Sprint(log[i], " ")
	}
	str += "]"
	return str
}

func ArgsToString(args *AppendEntriesArgs) string {
	str := "AppendEntriesArgs["
	str += fmt.Sprint("Term = ", args.Term, ", ")
	str += fmt.Sprint("LeaderId = ", args.LeaderId, ", ")
	str += fmt.Sprint("PrevLogIndex = ", args.PrevLogIndex, ", ")
	str += fmt.Sprint("PrevLogTerm = ", args.PrevLogTerm, ", ")
	str += fmt.Sprint("Entries = ", LogToString(args.Entries), ", ")
	str += fmt.Sprint("LeaderCommit = ", args.LeaderCommit)
	str += "]"
	return str
}

func ReplyToString(reply *AppendEntriesReply) string {
	str := "AppendEntriesReply["
	str += fmt.Sprint("Term = ", reply.Term, ", ")
	str += fmt.Sprint("Success = ", reply.Success)
	str += "]"
	return str
}
