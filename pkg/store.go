package dogenet

import (
	"encoding/gob"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"strconv"
	"sync"

	"github.com/dogeorg/dogenet/pkg/msg"
	"github.com/dogeorg/dogenet/pkg/seeds"
)

type NodeAddressMap map[string]NodeInfo
type NodeInfo struct {
	Time     uint32
	Services msg.LocalNodeServices
}

type NetMapState struct {
	Nodes     NodeAddressMap // node address -> timstamp, services
	NewNodes  []string       // queue of newly discovered nodes (high priority)
	SeedNodes []string       // queue of seed nodes (low priority)
}

type NetMap struct {
	mu     sync.Mutex
	state  NetMapState
	sample []string // a random sample of known nodes (not saved)
}

func NewNetMap() NetMap {
	return NetMap{state: NetMapState{Nodes: make(NodeAddressMap)}}
}

func (t *NetMap) Stats() (mapSize int, newNodes int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.state.Nodes), len(t.state.NewNodes)
}

func (t *NetMap) AddNode(address net.IP, port uint16, time uint32, services msg.LocalNodeServices) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := address.String() + "/" + strconv.Itoa(int(port)) // maybe use 18-byte IP:Port
	old, found := Map.state.Nodes[key]
	if !found || time > old.Time {
		// insert or replace
		Map.state.Nodes[key] = NodeInfo{
			Time:     time,
			Services: services,
		}
	}
	if !found {
		Map.state.NewNodes = append(Map.state.NewNodes, key)
	}
}

func (t *NetMap) AddNewNode(address net.IP, port uint16) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := address.String() + "/" + strconv.Itoa(int(port)) // maybe use 18-byte IP:Port
	Map.state.NewNodes = append(Map.state.NewNodes, key)
}

func (t *NetMap) ChooseNode() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	fmt.Printf("Queue: %d new, %d sampled, %d seeds, %d Map\n", len(t.state.NewNodes), len(t.sample), len(t.state.SeedNodes), len(t.state.Nodes))
	for {
		// highest priority: connect to newly discovered nodes.
		if len(t.state.NewNodes) > 0 {
			var addr string
			t.state.NewNodes, addr = pluckRandom(t.state.NewNodes)
			return addr
		}
		// next priority: connect to a random sample of known nodes.
		if len(t.sample) > 0 {
			var addr string
			t.sample, addr = pluckRandom(t.sample)
			return addr
		}
		// generate another sample of known nodes (XXX cull first)
		fmt.Println("Sampling the network map.")
		t.sampleNodeMap()
		if len(t.sample) > 0 {
			continue
		}
		// lowest priority: connect to seed nodes.
		if len(t.state.SeedNodes) > 0 {
			var addr string
			t.state.SeedNodes, addr = pluckRandom(t.state.SeedNodes)
			return addr
		}
		// fetch new seed nodes from DNS.
		// t.seedFromDNS()
		if len(t.state.SeedNodes) < 1 {
			t.seedFromFixed()
		}
	}
}

func pluckRandom(arr []string) ([]string, string) {
	len := len(arr)
	idx := rand.Intn(len)
	val := arr[idx]
	arr[idx] = arr[len-1] // copy down last elem
	arr = arr[:len-1]     // remove last elem
	return arr, val
}

func (t *NetMap) sampleNodeMap() {
	// choose 100 or so nodes at random from the nodeMap
	nodeMap := Map.state.Nodes
	mod := len(nodeMap) / 100
	if mod < 1 {
		mod = 1
	}
	idx := 0
	samp := rand.Intn(mod) // initial sample
	for key := range nodeMap {
		if idx >= samp {
			t.sample = append(t.sample, key)
			samp = idx + 1 + rand.Intn(mod) // next sample
		}
		idx++
	}
}

func (t *NetMap) seedFromDNS() {
	for _, node := range seeds.DNSSeeds {
		ips, err := net.LookupIP(node)
		if err != nil {
			fmt.Println("Error resolving DNS:", node, ":", err)
			continue
		}
		for _, ip := range ips {
			key := ip.String() + "/22556"
			t.state.SeedNodes = append(t.state.SeedNodes, key)
			fmt.Println("Seed from DNS:", key)
		}
	}
}

func (t *NetMap) seedFromFixed() {
	for _, seed := range seeds.FixedSeeds {
		key := net.IP(seed.Host).String() + "/" + strconv.Itoa(seed.Port)
		t.state.SeedNodes = append(t.state.SeedNodes, key)
		fmt.Println("Seed from Fixture:", key)
	}
}

func (t *NetMap) ReadGob(path string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open file %q: %w", path, err)
	}
	defer file.Close()
	decoder := gob.NewDecoder(file)
	if err := decoder.Decode(&t.state); err != nil {
		if err == io.EOF {
			return fmt.Errorf("file %q is empty", path)
		}
		return fmt.Errorf("cannot decode object from file %q: %w", path, err)
	}
	return nil
}

func (t *NetMap) WriteGob(path string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	tempFile, err := os.CreateTemp("", "temp_gob_file")
	if err != nil {
		return fmt.Errorf("cannot create temporary file: %w", err)
	}
	defer os.Remove(tempFile.Name())
	encoder := gob.NewEncoder(tempFile)
	if err := encoder.Encode(t.state); err != nil {
		return fmt.Errorf("cannot encode object: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("cannot close temporary file: %w", err)
	}
	if err := os.Rename(tempFile.Name(), path); err != nil {
		return fmt.Errorf("cannot rename temporary file to %q: %w", path, err)
	}
	return nil
}