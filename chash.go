package chash4go

import (
	"errors"
	"fmt"
	"go_lib"
	"runtime/debug"
)

type NodeCheckFunc func(target string) bool

type HashRingStatus string

// Hash ring status
const (
	UNINITIALIZED HashRingStatus = "UNINITIALIZED"
	INITIALIZED   HashRingStatus = "INITIALIZED"
	BUILDED       HashRingStatus = "BUILDED"
	DESTROYED     HashRingStatus = "DESTROYED"
)

type HashRing interface {
	Build(shadowNumber uint16) error
	Destroy() error
	Status() HashRingStatus
	Check(nodeCheckFunc NodeCheckFunc) error
	StartCheck(nodeCheckFunc NodeCheckFunc, intervalSeconds uint16) (bool, error)
	StopCheck() (bool, error)
	InChecking() bool
	AddTarget(target string) error
	RemoveTarget(target string) error
	GetTarget(key string) (string, error)
}

type SimpleHashRing struct {
	nodeRing         *NodeRing
	targetMap        map[string][]uint64
	pendingTargetMap map[string][]uint64
	changeSign       *go_lib.RWSign
	shadowNumber     uint16
	checker          Checker
	status           HashRingStatus
}

func (self *SimpleHashRing) initialize() {
	self.nodeRing = NewNodeRing()
	self.targetMap = make(map[string][]uint64, 0)
	self.pendingTargetMap = make(map[string][]uint64, 0)
	self.shadowNumber = uint16(1000)
	self.status = INITIALIZED
}

func (self *SimpleHashRing) Build(shadowNumber uint16) error {
	self.getChangeSign().Set()
	defer func() {
		self.getChangeSign().Unset()
		if err := recover(); err != nil {
			errorMsg := fmt.Sprintf("Occur FATAL error when build hash ring: %s", err)
			go_lib.LogFatalln(errorMsg)
			debug.PrintStack()
		}
	}()
	switch self.status {
	case "", UNINITIALIZED, DESTROYED:
		self.initialize()
		fallthrough
	case INITIALIZED:
		if shadowNumber > 0 {
			self.shadowNumber = shadowNumber
		}
		self.status = BUILDED
	default:
		errorMsg := "Please destroy hash ring before rebuilding."
		go_lib.LogErrorln(errorMsg)
		return errors.New(errorMsg)
	}
	return nil
}

func (self *SimpleHashRing) Destroy() error {
	self.getChangeSign().Set()
	defer func() {
		self.getChangeSign().Unset()
		if err := recover(); err != nil {
			errorMsg := fmt.Sprintf("Occur FATAL error when build hash ring: %s", err)
			go_lib.LogFatalln(errorMsg)
			debug.PrintStack()
		}
	}()
	switch self.status {
	case INITIALIZED, BUILDED:
		self.nodeRing = nil
		self.targetMap = nil
		self.pendingTargetMap = nil
		self.changeSign = nil
		self.shadowNumber = uint16(0)
		self.StopCheck()
		self.status = DESTROYED
	default:
		warningMsg := "The hash ring were not builded. IGNORE the destroy operation."
		go_lib.LogWarnln(warningMsg)
	}
	return nil
}

func (self *SimpleHashRing) Status() HashRingStatus {
	if len(self.status) == 0 {
		self.status = UNINITIALIZED
	}
	return self.status
}

func (self *SimpleHashRing) Check(nodeCheckFunc NodeCheckFunc) error {
	defer func() {
		if err := recover(); err != nil {
			errorMsg := fmt.Sprintf("Occur FATAL error when check node ring: %s", err)
			go_lib.LogFatalln(errorMsg)
			debug.PrintStack()
		}
	}()
	for target, nodeKeys := range self.targetMap {
		if !nodeCheckFunc(target) {
			go_lib.LogInfof("Removing invalid target '%s'...", target)
			if self.removeNodeByKeys(self.nodeRing, nodeKeys) {
				self.pendingTargetMap[target] = nodeKeys
				self.targetMap[target] = nil
			}
		}
	}
	for target, nodeKeys := range self.pendingTargetMap {
		if nodeCheckFunc(target) {
			go_lib.LogInfof("Adding valid target '%s'...", target)
			validNodeKeys, done := self.addNodesOfTarget(self.nodeRing, target, nodeKeys)
			if done {
				self.targetMap[target] = validNodeKeys
				self.pendingTargetMap[target] = nil
			}
		}
	}
	return nil
}

func (self *SimpleHashRing) StartCheck(nodeCheckFunc NodeCheckFunc, intervalSeconds uint16) (bool, error) {
	defer func() {
		if err := recover(); err != nil {
			errorMsg := fmt.Sprintf("Occur FATAL error when start checker: %s", err)
			go_lib.LogFatalln(errorMsg)
			debug.PrintStack()
		}
	}()
	if self.status != BUILDED {
		go_lib.LogWarnln("The hash ring were not builded. IGNORE the checker startup.")
		return false, nil
	}
	checkFunc := func() {
		err := self.Check(nodeCheckFunc)
		if err != nil {
			go_lib.LogErrorf("Node ring checking is FAILING: %s\n", err)
		}
	}
	if self.checker != nil && self.checker.InChecking() {
		go_lib.LogInfoln("Stop checker before reinitialization.")
		self.checker.Stop()
	}
	self.checker = NewChecker(intervalSeconds)
	result := self.checker.Start(checkFunc)
	return result, nil
}

func (self *SimpleHashRing) StopCheck() (bool, error) {
	defer func() {
		if err := recover(); err != nil {
			errorMsg := fmt.Sprintf("Occur FATAL error when stop checker: %s", err)
			go_lib.LogFatalln(errorMsg)
			debug.PrintStack()
		}
	}()
	if self.checker == nil {
		return false, nil
	}
	result := self.checker.Stop()
	return result, nil
}

func (self *SimpleHashRing) InChecking() bool {
	if self.checker == nil {
		return false
	}
	return self.checker.InChecking()
}

func (self *SimpleHashRing) AddTarget(target string) (bool, error) {
	defer func() {
		if err := recover(); err != nil {
			errorMsg := fmt.Sprintf("Occur FATAL error when add target: %s", err)
			go_lib.LogFatalln(errorMsg)
			debug.PrintStack()
		}
	}()
	currentShadowNumber := int(self.shadowNumber)
	targetShadows := make([]string, currentShadowNumber)
	for i := 0; i < currentShadowNumber; i++ {
		targetShadows[i] = fmt.Sprintf("%s-%d", target, i)
	}
	total := (currentShadowNumber * KETAMA_NUMBERS_LENGTH)
	nodeAll := make([]Node, total)
	nodeKeyAll := make([]uint64, total)
	count := 0
	for _, targetShadow := range targetShadows {
		nodeKeys := GetKetamaNumbers(targetShadow)
		for _, nodeKey := range nodeKeys {
			nodeAll[count] = Node{nodeKey, target}
			nodeKeyAll[count] = nodeKey
			count++
		}
	}
	validNodeKeys, done := self.addNodes(self.nodeRing, nodeAll...)
	if done {
		self.targetMap[target] = validNodeKeys
	}
	return done, nil
}

func (self *SimpleHashRing) RemoveTarget(target string) (bool, error) {
	defer func() {
		if err := recover(); err != nil {
			errorMsg := fmt.Sprintf("Occur FATAL error when remove target: %s", err)
			go_lib.LogFatalln(errorMsg)
			debug.PrintStack()
		}
	}()
	nodeKeys := self.targetMap[target]
	if nodeKeys == nil || len(nodeKeys) == 0 {
		return false, nil
	}
	self.removeNodeByKeys(self.nodeRing, nodeKeys)
	delete(self.targetMap, target)
	delete(self.pendingTargetMap, target)
	return true, nil
}

func (self *SimpleHashRing) GetTarget(key string) (string, error) {
	self.getChangeSign().RSet()
	defer func() {
		self.getChangeSign().RUnset()
		if err := recover(); err != nil {
			errorMsg := fmt.Sprintf("Occur FATAL error when get target of key '%s': %s", key, err)
			go_lib.LogFatalln(errorMsg)
			debug.PrintStack()
		}
	}()
	nodeKey := GetHashForKey(key)
	matchedNode := self.nodeRing.Next(nodeKey)
	if matchedNode == nil {
		return "", nil
	}
	return matchedNode.Target, nil
}

func (self *SimpleHashRing) addNodes(nodeRing *NodeRing, nodes ...Node) ([]uint64, bool) {
	self.getChangeSign().Set()
	defer self.getChangeSign().Unset()
	return nodeRing.Add(nodes...)
}

func (self *SimpleHashRing) addNodesOfTarget(nodeRing *NodeRing, target string, nodeKeys []uint64) ([]uint64, bool) {
	self.getChangeSign().Set()
	defer self.getChangeSign().Unset()
	nodes := make([]Node, len(nodeKeys))
	for i, nodeKey := range nodeKeys {
		nodes[i] = Node{nodeKey, target}
	}
	return nodeRing.Add(nodes...)
}

func (self *SimpleHashRing) removeNodeByKeys(nodeRing *NodeRing, nodeKeys []uint64) bool {
	self.getChangeSign().Set()
	defer self.getChangeSign().Unset()
	result := true
	for _, nodeKey := range nodeKeys {
		result = nodeRing.Remove(nodeKey) && result
	}
	return result
}

func (self *SimpleHashRing) getChangeSign() *go_lib.RWSign {
	if self.changeSign == nil {
		self.changeSign = go_lib.NewRWSign()
	}
	return self.changeSign
}
