package dht

//import "lang"
import "log"
import "github.com/pmylund/sortutil"

type DhtNode struct {
	IpAddr string
	nodeId ID // sha1(ip)
	routingTable [IDLen][]RoutingEntry // map from nodeId to IP- a IDLen X K matrix
	// set routing table cap to bucket := make([]RoutingEntry, 0,K)
	kv map[string]string // map from username to IP
}

func moveToEnd(slice []RoutingEntry, index int) []RoutingEntry{
	return append(slice[:index], append(slice[index + 1:], slice[index])...)
}

//this gets called when another node is contacting this node through any API method!
func (node *DhtNode) updateRoutingTable(nodeId ID, IpAddr string) {
	// ordering of K bucket is from LRS to MRS
	entry := RoutingEntry{nodeId: nodeId, IpAddr: IpAddr}
	n := find_n(nodeId, node.nodeId) // n is the bucket index- index of first bit that doesn't match
	bucket := node.routingTable[n]
	defer func(){node.routingTable[n] = bucket}()
	//check if node is in routing table
	for idx, r_entry := range(bucket){
		if r_entry == entry{
			bucket = moveToEnd(bucket, idx)
			return
		}
	} // new entry is not in bucket
	if len(bucket) < K { // bucket is not full
		bucket = append(bucket, entry)
	} else { // bucket is full
		// ping the front of list (LRS)
		if ! node.Ping(bucket[0].IpAddr){
			bucket[0] = entry //if does not respond, replace
		}
		bucket = moveToEnd(bucket, 0)	// move to end
	}
}

// get the alpha closest nodes to node ID in order to find user/node
// returns a slice of RoutingEntriesDist sorted in increasing order of dist from 
func (node *DhtNode) getClosest(target_result_len int, targetNodeId ID) []RoutingEntry{
	res := make([]RoutingEntryDist, 0, target_result_len)
	orig_bucket_idx := find_n(targetNodeId, node.nodeId)
	bucket_idx := orig_bucket_idx
	increasing_bucket := true
	for len(res) < target_result_len{ //need to keep looping over buckets until res is filled
		bucket := node.routingTable[bucket_idx]
		for _, value := range(bucket){
			xor := Xor(targetNodeId, value.nodeId)
			if len(res) < target_result_len {
				res = append(res, RoutingEntryDist{routingEntry: value, dist: xor})
			}else{ //bucket is full	
				res = sortutil.AscByField(res, "dist")
				if xor < res[len(res) - 1].dist{
					res[len(res) - 1] = RoutingEntryDist{routingEntry: value, dist: xor}
				}
			}
		}
		if bucket_idx < IDLen - 1 && increasing_bucket{ // starts increasing
			bucket_idx++
		} else if bucket_idx == IDLen - 1 && increasing_bucket{ // stops increasing
			increasing_bucket = false
			bucket_idx = orig_bucket_idx - 1
		} else if bucket_idx == 0 && ! increasing_bucket{
			break
		}else {
			bucket_idx--
		}
	}
	return sortutil.AscByField(res, "dist")
}

// AnnouceUser RPC handlers
func (node *DhtNode) AnnouceUserHandler(args *AnnouceUserArgs, reply *AnnouceUserReply) error {
	node.updateRoutingTable(args.QueryingNodeId, args.QueryingIpAddr)
	return nil
}

// AnnouceUser API
func (node *DhtNode) AnnounceUser(username string, IpAddr string) {
}

// FindNode RPC handlers
// all this does is call getClosest on K nodes
// returns k sorted slice of RoutingEntryDist from my routing table
func (node *DhtNode) FindNodeHandler(args *FindNodeArgs, reply *FindNodeReply) error {
	node.updateRoutingTable(args.QueryingNodeId, args.QueryingIpAddr)
	return nil
}

// helper function called by both FindUser and AnnounceUser
// returns a k-length slice of RoutingEntriesDist sorted in increasing order of dist from 
func (node *DhtNode) nodeLookup(nodeId ID) {
	// get the closest nodes to the desired node ID
	// then add to a stack. we'll 
	closestNodes := node.getClosest(Alpha, nodeId)
	triedNodes := make(map[ID]bool)

	// send the initial min(Alpha, # of closest Node)
	// messages in flight to start the process
	replyChannel := make(chan *FindNodeReply, Alpha)
	sent := 0
	for _, entryDist := range closestNodes{
		go node.sendFindNodeQuery(entryDist.routingEntry, replyChannel)
		triedNodes[entryDist.routingEntry.nodeId] = true
		sent++
	}

	// now process replies as they arrive, spinning off new
	// requests up to alpha requests
	for {
		reply := <-replyChannel
		// process the reply, see if we are done
		// if we need to break because of stop cond: send done channel
		combined := append(closestNodes, reply.TryNodes...)
		combined = sortutil.AscByField(combined, "dist")[:min(K, len(combined))]
		if combined == closestNodes{ //closest Nodes have not changed
			return closestNodes
		}
		closestNodes = combined
		sent--

		// then check to see if we are still under
		// the total of alpha messages still in flight
		// and if so, send more
		for i := sent; i < Alpha; i++ {
			for idx, entryDist := range closestNodes{
				//find first value not in tried nodes
				_, already_tried := triedNodes[entryDist.routingEntry.nodeId]
				if ! already_tried{
					go node.sendFindNodeQuery(entryDist.RoutingEntry, replyChannel)
					sent++
					break
				}
			}			
		}		
	}	
}

func (node *DhtNode) sendFindNodeQuery(entry RoutingEntry, replyChannel chan *FindNodeReply) {
	/*
		This function is generally called as a separate goroutine. At the end of the call, 
		the reply is added to the done Channel, which is read by a separate thread. 
	*/
	ok := false
	args := &FindNodeArgs{QueryingNodeId: node.nodeId, QueryingIpAddr: "???", TargetNodeId: entry.nodeId}
	var reply FindNodeReply
	
	for !ok {
		ok = call(entry.IpAddr, "DhtNode.FindNodeHandler", args, &reply)
		if !ok {
			log.Printf("Failed! Will try again.")
		}
	}

	// add refernce to reply onto the channel
	replyChannel <- &reply
}

// GetUser RPC handlers
func (node *DhtNode) GetUserHandler(args *GetUserArgs, reply *GetUserReply) error {
	node.updateRoutingTable(args.QueryingNodeId, args.QueryingIpAddr)
	return nil
}

// GetUser RPC API
// returns IP of username
func (node *DhtNode) GetUser(username string) string {

	return ""
}

// Ping RPC handlers
func (node *DhtNode) PingHandler(args *PingArgs, reply *PingReply) error {
	node.updateRoutingTable(args.QueryingNodeId, args.QueryingIpAddr)
	reply.QueriedNodeId = node.nodeId
	return nil
}

// Ping RPC API
//assume you already have them in routing table
func (node *DhtNode) Ping(IpAddr string) bool{
	args := &PingArgs{QueryingNodeId: node.nodeId}
	var reply PingReply
	ok := call(IpAddr, "DhtNode.PingHandler", args, &reply)
	return ok
}


func MakeNode(myIpAddr string, routingTable [IDLen][]RoutingEntry) *DhtNode{
	node := &DhtNode{IpAddr: myIpAddr, nodeId: Sha1(myIpAddr), routingTable: routingTable}
	node.kv = make(map[string]string)
	return node
}