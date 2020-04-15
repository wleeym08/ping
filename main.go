package main

import (
   "fmt"
   "strings"
   "time"
   "math"
   "encoding/binary"
   "flag"
   "os"
   "os/signal"
   "syscall"
   "golang.org/x/net/icmp"
   "golang.org/x/net/ipv4"
   "golang.org/x/net/ipv6"
   "net"
)

type statsData struct {
    trans int
    recv int
    rtts []float64
}

// Resolve the given host to get the IP address
func resolve(host string) (ip net.IP, isIPv6 bool) {
    isIPv6 = false
    ip = net.ParseIP(host)

    if ip == nil {
       ips, err := net.LookupIP(host)
       if err != nil {
          return
      } else {
          ip = ips[0]
      }
    } 

    if ip.To4() == nil {
        isIPv6 = true
    }

    return
}

// Compose an echo message
func echo(seq int, isIPv6 bool, dataSize int) (data []byte, err error) {
    now := time.Now().UnixNano()
    timestamp := make([]byte, 8)
    binary.LittleEndian.PutUint64(timestamp, uint64(now))
    padding := []byte(strings.Repeat(" ", dataSize - 8))

    msg := icmp.Message{
        Code: 0,
        Body: &icmp.Echo{
            ID: os.Getpid() & 0xffff,
            Seq: seq,
            Data: append(timestamp, padding...),
        },
    }

    if isIPv6 {
        msg.Type = ipv6.ICMPTypeEchoRequest
    } else {
        msg.Type = ipv4.ICMPTypeEcho
    }

    data, err = msg.Marshal(nil)
    if err != nil {
        fmt.Println("Error: Failed to marshal echo")
    }

    return
}

// Ping for specified times until receiving interrupt signal
func pingForTimes(ip string, isIPv6 bool, dataSize int, count int, s *statsData,
    done chan bool) {
    for i := 0; i < count; i++ {
        ttl, rtt, err := pingOnce(i, ip, isIPv6, dataSize)
        if err != nil {
            fmt.Println("Request timeout for icmp_seq", i)
        } else {
            s.recv++
            s.rtts = append(s.rtts, rtt)
            var param string
            if isIPv6 {
                param = "hlim"
            } else {
                param = "ttl"
            }
            fmt.Printf("Packet from %v: icmp_seq=%v %v=%v time=%v ms\n",
                ip, i, param, ttl, rtt)
        }
        s.trans++
        time.Sleep(time.Second)

        select {
        case <-done:
            return
        default:
        }
    }
    return 
}

// Ping forever until receiving interrupt signal
func pingForever(ip string, isIPv6 bool, dataSize int, s *statsData, done chan bool) {
    for i := 0; ; i++ {
        ttl, rtt, err := pingOnce(i, ip, isIPv6, dataSize)
        if err != nil {
            fmt.Println("Request timeout for icmp_seq", i)
        } else {
            s.recv++
            s.rtts = append(s.rtts, rtt)
            var param string
            if isIPv6 {
                param = "hlim"
            } else {
                param = "ttl"
            }
            fmt.Printf("Packet from %v: icmp_seq=%v %v=%v time=%v ms\n",
                ip, i, param, ttl, rtt)
        }
        s.trans++
        time.Sleep(time.Second)

        select {
        case <-done:
            return
        default:
        }
    }
    return 
}

// Send an ICMP echo request, wait for the reply
func pingOnce(seq int, ip string, isIPv6 bool, dataSize int) (ttl int, rtt float64, err error) {
    ttl = 0
    rtt = 0.0
    err = nil
    timeout := time.Second
    var c net.Conn

    if isIPv6 {
        c, err = net.Dial("ip6:ipv6-icmp", ip)
    } else {
        c, err = net.Dial("ip4:icmp", ip)
    }

    if err != nil {
        fmt.Println("Error: Failed to dial destination", err)
        return 
    }
    defer c.Close()

    echoMsg, err := echo(seq, isIPv6, dataSize)
    size, err := c.Write(echoMsg)
    if err != nil {
        return
    }

    var data []byte
    if isIPv6 {
        data = make([]byte, size)
    } else {
        data = make([]byte, size + 20)
    }
    
    startTime := time.Now()
    c.SetReadDeadline(startTime.Add(timeout))

    for time.Now().Sub(startTime) < timeout {
        _, err = c.Read(data)
        if err != nil {
            return
        }

        var replyMsg *icmp.Message
        if isIPv6 {
            var header *ipv6.Header
            header, err = ipv6.ParseHeader(data)
            if err != nil {
                return
            }
            ttl = header.HopLimit
            replyMsg, err = icmp.ParseMessage(58, data)  
            if err != nil {
                return
            }

        } else {
            var header *ipv4.Header
            header, err = icmp.ParseIPv4Header(data)
            if err != nil {
                return
            }
            ttl = header.TTL
            replyMsg, err = icmp.ParseMessage(1, data[header.Len:])  
            if err != nil {
                return
            }
        }
    
        var body []byte
        if replyMsg.Type == ipv4.ICMPTypeEchoReply {
            body, err = replyMsg.Body.Marshal(1)
        } else if replyMsg.Type == ipv6.ICMPTypeEchoReply {
            body, err = replyMsg.Body.Marshal(58)
        } else {
            continue
        }
        body = body[4:]

        rtt = float64(time.Now().UnixNano() - 
            int64(binary.LittleEndian.Uint64(body[:8]))) / 1000000.0
        return
    }
    return   
}

// Print statistics at the end of the program
func stats(s *statsData) {
    rttMin, rttMax, rttAvg, rttStd := 0.0, 0.0, 0.0, 0.0
    if s.recv > 0 {
        rttMin, rttMax, rttAvg = s.rtts[0], s.rtts[0], s.rtts[0]
        for i := 1; i < s.recv; i++ {
            if s.rtts[i] < rttMin {
                rttMin = s.rtts[i]
            }
            if s.rtts[i] > rttMax {
                rttMax = s.rtts[i]
            }
            rttAvg += s.rtts[i]
        }
        rttAvg /= float64(s.recv)

        for i := 0; i < s.recv; i++ {
            rttStd += (s.rtts[i] - rttAvg) * (s.rtts[i] - rttAvg)
        }
        rttStd = math.Sqrt(rttStd / float64(s.recv))
    }

    fmt.Println("\n--- Statistics ---")
    fmt.Printf("%v packets transmitted, %v packets received, %.3f%% packet loss\n",
        s.trans, s.recv, (1 - float64(s.recv) / float64(s.trans)) * 100)
    fmt.Printf("round-trip min/avg/max/std-dev = %.3f/%.3f/%.3f/%.3f ms\n", 
        rttMin, rttAvg, rttMax, rttStd)
}

func main() {
    size := 56
    count := flag.Int("c", 0, "the count of echo requests")
    flag.Usage = func() {
        fmt.Println("usage: ping [-c count] host")
    }
    flag.Parse()
    
    if len(flag.Args()) != 1 {
        flag.Usage()
        return
    }

    host := flag.Args()[0]
    ip, isIPv6 := resolve(host)
    if ip == nil {
        fmt.Println("ping: unknown host")
        return
    }

    sigCh := make(chan os.Signal, 1)
    done := make(chan bool, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

    go func() {
       <-sigCh
       done <- true
    }()

    s := statsData{
        trans: 0,
        recv: 0,
        rtts: make([]float64, 0),
    }

    fmt.Printf("PING %v (%v): %v data bytes\n", host, ip.String(), size)
    if *count != 0 {
        if (*count < 0) {
            fmt.Println("ping: count must be a positive number")
            return
        }
        pingForTimes(ip.String(), isIPv6, size, *count, &s, done)
    } else {
        pingForever(ip.String(), isIPv6, size, &s, done)
    }
    stats(&s)
}
