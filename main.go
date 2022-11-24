package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/lithammer/shortuuid/v4"
)

type Workload struct {
	workload     []byte
	WorkloadSize uint64 `json:"workloadSize"`
	ChunkSize    uint64 `json:"chunkSize"`
}

var workload Workload = Workload{
	workload:     []byte{},
	WorkloadSize: 100,
	ChunkSize:    0,
}

func main() {
	rand.Seed(time.Now().UnixNano())

	main := "localhost:8081"
	if len(os.Args) == 2 {
		main = os.Args[1]
	}
	resp, err := http.Get("http://" + main + "/register")
	instance := InstanceInfo{}
	if err != nil {
		fmt.Printf("Can't connect to main instance at %s, assuming there is none and acting as main", main)

		runMain()
		instance = InstanceInfo{Id: "main", InternalAddress: GetOutboundIP().String() + ":8082", lastActivity: time.Now()}
		instances = append(instances, instance)
		workload.ChunkSize = workload.WorkloadSize
	} else {
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			panic(fmt.Errorf("error connecting to main: %v", err))

		}
		err = json.Unmarshal(body, &instances)
		if err != nil {
			panic(fmt.Errorf("error parsing instance: %v", err))
		}
		instance = instances[len(instances)-1]

	}
	runWorker(instance)
	printSize(instance)

}

func GetOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}

type InstanceInfo struct {
	Id              string `json:"id"`
	InternalAddress string `json:"internalAddress"`
	lastActivity    time.Time
}

var instancesMutex sync.Mutex
var instances []InstanceInfo

func runMain() {

	serverMain := http.NewServeMux()
	serverMain.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		instancesMutex.Lock()
		instance := InstanceInfo{
			Id:              shortuuid.New(),
			InternalAddress: strings.Split(r.RemoteAddr, ":")[0] + ":8082",
			lastActivity:    time.Now(),
		}
		instances = append(instances, instance)
		workload.ChunkSize = workload.WorkloadSize / uint64((len(instances)))
		resp, err := json.Marshal(instances)
		if err != nil {
			fmt.Printf("error creating json response: %v\n", err)
		}
		w.Write(resp)
		instancesMutex.Unlock()
	})
	serverMain.HandleFunc("/getChunk", func(w http.ResponseWriter, r *http.Request) {
		instanceId := r.URL.Query()["id"][0]
		instancesMutex.Lock()
		for i := 0; i < len(instances); i++ {
			if instances[i].Id == instanceId {
				instances[i].lastActivity = time.Now()
			}
		}
		instancesMutex.Unlock()
		resp, err := json.Marshal(workload)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf("Can't marshal workload: %v", err)))
			return
		}
		w.Write([]byte(resp))

	})
	serverMain.HandleFunc("/getInstanceInfo", func(w http.ResponseWriter, r *http.Request) {
		resp, err := json.Marshal(instances)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf("Can't marshal instance info: %v", err)))
			return
		}
		w.Write([]byte(resp))
	})
	serverMain.HandleFunc("/set", func(w http.ResponseWriter, r *http.Request) {
		newmem := r.URL.Query()["mem"][0]
		i, err := strconv.Atoi(newmem)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(fmt.Sprintf("Error parsing memory: %v", err)))
			return
		}
		workload.WorkloadSize = uint64(i)
		workload.ChunkSize = workload.WorkloadSize / uint64((len(instances)))
	})
	go updateInstances()
	go http.ListenAndServe(":8082", serverMain)
	for {
		fmt.Println("Waiting for server to be responding...")
		_, err := http.Get("http://localhost:8082/getInstanceInfo")
		if err == nil {
			break
		}
		time.Sleep(1 * time.Second)

	}
}

func updateInstances() {
	for {
		time.Sleep(10 * time.Second)
		for i := 0; i < len(instances); i++ {
			if time.Since(instances[i].lastActivity) > 20*time.Second {
				instancesMutex.Lock()
				removeInstance(i)
				instancesMutex.Unlock()
			}
		}
		workload.ChunkSize = workload.WorkloadSize / uint64((len(instances)))
		fmt.Printf("%d instances: %v\n", len(instances), instances)
	}
}
func removeInstance(i int) {
	instancesMutex.Lock()
	instances = append(instances[:i], instances[i+1:]...)
	instancesMutex.Unlock()
	workload.ChunkSize = workload.WorkloadSize / uint64((len(instances)))
}

func getMainInstance() InstanceInfo {
	for i := 0; i < len(instances); i++ {
		if instances[i].Id == "main" {
			return instances[i]
		}
	}
	return InstanceInfo{}
}

func runWorker(instance InstanceInfo) {
	fmt.Printf("Instance ID: %s\n", instance.Id)

	serverWorker := http.NewServeMux()
	serverWorker.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		resp, err := http.Get("http://" + getMainInstance().InternalAddress + "/register")
		if err != nil {
			fmt.Printf("Error forwarding request to register: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("Error reading response from /register request: %v\n", err)
			return
		}
		w.Write(body)
	})
	serverWorker.HandleFunc("/set", func(w http.ResponseWriter, r *http.Request) {
		newmem := r.URL.Query()["mem"][0]
		resp, err := http.Get("http://" + getMainInstance().InternalAddress + "/set?mem=" + newmem)
		if err != nil {
			fmt.Printf("Error forwarding request to set: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("Error reading response from /set request: %v\n", err)
			return
		}
		w.Write(body)
	})
	serverWorker.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		body := ""
		body += fmt.Sprintf("instances_count %d\n", len(instances))
		body += fmt.Sprintf("workload_mib %d\n", workload.WorkloadSize)
		body += fmt.Sprintf("chunksize_mib %d\n", workload.ChunkSize)
		w.Write([]byte(body))
	})

	go startWorkerServer(serverWorker)

}
func startWorkerServer(serverWorker *http.ServeMux) {
	err := http.ListenAndServe(":8081", serverWorker)
	if err != nil {
		fmt.Printf("Can't run worker server, assuming we run on one host: %v", err)
	}
}
func updateWorkload() {
	fmt.Printf("Allocating %d Mi\n", workload.ChunkSize)
	if len(workload.workload) != int(workload.ChunkSize*1024*1024) {
		workload.workload = make([]byte, workload.ChunkSize*1024*1024)
		for i := 0; i < len(workload.workload); i++ {
			workload.workload[i] = byte(rand.Intn(255))
		}
	}
	runtime.GC()
}

func takoverMain(instance *InstanceInfo) {
	fmt.Printf("Assuming main is not responding anymore. Checking who can take over.\n")

	if len(instances) == 1 {
		panic(fmt.Errorf("no instance left. Exiting"))
	}

	removeInstance(0)
	fmt.Printf("New main: %s My ID: %s\n", instances[0].Id, instance.Id)
	if instances[0].Id == instance.Id {
		fmt.Println("Taking over...")
		instances[0].Id = "main"
		instance.Id = "main"
		runMain()
		runWorker(*instance)
		return
	}
	fmt.Println("Not my call, giving the new main a bit to settle...")
	instances[0].Id = "main"
	time.Sleep(5 * time.Second)
}

func printSize(instance InstanceInfo) {
	for {
		if instance.Id != "main" {
			resp, err := http.Get("http://" + getMainInstance().InternalAddress + "/getInstanceInfo")
			if err != nil {
				fmt.Printf("Error sending request to getInstanceInfo: %v\n", err)
				takoverMain(&instance)
				continue
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				fmt.Printf("Error reading response from /getInstanceInfo request: %v\n", err)
				return
			}
			err = json.Unmarshal(body, &instances)
			if err != nil {
				fmt.Printf("Error unmarshalling response from /getInstanceInfo request: %v\n", err)
				return
			}
		}

		resp, err := http.Get("http://" + getMainInstance().InternalAddress + "/getChunk?id=" + instance.Id)
		if err != nil {
			fmt.Printf("Error querying chunk size: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("Error reading response from /getChunk request: %v\n", err)
			return
		}

		newWorkload := Workload{}
		err = json.Unmarshal(body, &newWorkload)
		if err != nil {
			fmt.Printf("Error parsing chunk: %v\n", err)
			os.Exit(1)
		}
		//fmt.Println(string(body))
		if instance.Id != "main" {
			workload.WorkloadSize = newWorkload.WorkloadSize
			workload.ChunkSize = newWorkload.ChunkSize
		}
		if len(workload.workload) != int(newWorkload.ChunkSize)*1024*1024 {
			updateWorkload()
		}

		size := (unsafe.Sizeof(workload) + unsafe.Sizeof([1024 * 1024]byte{})*uintptr(workload.ChunkSize)) / (1024 * 1024)
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		fmt.Printf("Instance ID: %s Variable size: %d Mi Allocated: %d Mi\n", instance.Id, size, m.Alloc/(1024*1024))
		time.Sleep(1 * time.Second)
	}

}
