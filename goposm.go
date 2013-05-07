package main

import (
	"flag"
	"fmt"
	"goposm/cache"
	"goposm/element"
	"goposm/mapping"
	"goposm/parser"
	"goposm/stats"
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"
)

func parse(cache *cache.OSMCache, progress *stats.Statistics, filename string) {
	nodes := make(chan []element.Node)
	coords := make(chan []element.Node)
	ways := make(chan []element.Way)
	relations := make(chan []element.Relation)

	positions := parser.PBFBlockPositions(filename)

	waitParser := sync.WaitGroup{}
	for i := 0; i < runtime.NumCPU(); i++ {
		waitParser.Add(1)
		go func() {
			for pos := range positions {
				parser.ParseBlock(
					pos,
					coords,
					nodes,
					ways,
					relations,
				)
			}
			//runtime.GC()
			waitParser.Done()
		}()
	}

	waitCounter := sync.WaitGroup{}

	for i := 0; i < runtime.NumCPU(); i++ {
		waitCounter.Add(1)
		go func() {
			for ws := range ways {
				for _, w := range ws {
					mapping.WayTags.Filter(w.Tags)
				}
				cache.Ways.PutWays(ws)
				progress.AddWays(len(ws))
			}
			waitCounter.Done()
		}()
	}
	for i := 0; i < runtime.NumCPU(); i++ {
		waitCounter.Add(1)
		go func() {
			for rels := range relations {
				for _, r := range rels {
					mapping.RelationTags.Filter(r.Tags)
				}
				cache.Relations.PutRelations(rels)
				progress.AddRelations(len(rels))
			}
			waitCounter.Done()
		}()
	}
	for i := 0; i < runtime.NumCPU(); i++ {
		waitCounter.Add(1)
		go func() {
			for nds := range coords {
				cache.Coords.PutCoords(nds)
				progress.AddCoords(len(nds))
			}
			waitCounter.Done()
		}()
	}
	for i := 0; i < 2; i++ {
		waitCounter.Add(1)
		go func() {
			for nds := range nodes {
				for _, nd := range nds {
					ok := mapping.PointTags.Filter(nd.Tags)
					if !ok {
						nd.Tags = nil
					}
				}
				n, _ := cache.Nodes.PutNodes(nds)
				progress.AddNodes(n)
			}
			waitCounter.Done()
		}()
	}

	waitParser.Wait()
	close(coords)
	close(nodes)
	close(ways)
	close(relations)
	waitCounter.Wait()
}

var (
	cpuprofile     = flag.String("cpuprofile", "", "filename of cpu profile output")
	cachedir       = flag.String("cachedir", "/tmp/goposm", "cache directory")
	overwritecache = flag.Bool("overwritecache", false, "overwritecache")
	appendcache    = flag.Bool("appendcache", false, "append cache")
	read           = flag.String("read", "", "read")
	write          = flag.Bool("write", false, "write")
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	runtime.GOMAXPROCS(runtime.NumCPU())
	flag.Parse()

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	osmCache := cache.NewOSMCache(*cachedir)

	if *read != "" && osmCache.Exists() {
		if *overwritecache {
			log.Println("removing existing cache", *cachedir)
			err := osmCache.Remove()
			if err != nil {
				log.Fatal("unable to remove cache:", err)
			}
		} else if !*appendcache {
			log.Fatal("cache already exists use -appendcache or -overwritecache")
		}
	}

	err := osmCache.Open()
	if err != nil {
		log.Fatal(err)
	}
	defer osmCache.Close()

	fmt.Println("start")
	progress := stats.StatsReporter()

	if *read != "" {
		parse(osmCache, progress, *read)
		progress.Message("done reading")
	}

	if *write {
		progress.Reset()
		rel := osmCache.Relations.Iter()
		for _ = range rel {
			progress.AddRelations(1)
			// fmt.Println(r)
		}

		way := osmCache.Ways.Iter()

		diffCache := cache.NewDiffCache(*cachedir)
		if err = diffCache.Remove(); err != nil {
			log.Fatal(err)
		}
		if err = diffCache.Open(); err != nil {
			log.Fatal(err)
		}

		waitFill := sync.WaitGroup{}
		for i := 0; i < runtime.NumCPU(); i++ {
			waitFill.Add(1)

			go func() {
				for w := range way {
					progress.AddWays(1)
					ok := osmCache.Coords.FillWay(w)
					if !ok {
						continue
					}
					if true {
						for _, node := range w.Nodes {
							diffCache.Coords.Add(node.Id, w.Id)
						}
					}
				}
				waitFill.Done()
			}()
		}
		waitFill.Wait()
	}

	//parser.PBFStats(os.Args[1])
	fmt.Println("\ndone")
}