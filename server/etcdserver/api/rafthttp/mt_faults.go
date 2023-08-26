package rafthttp

import (
	"errors"
	"go.etcd.io/etcd/server/v3/daproto"
	"gonum.org/v1/gonum/floats"
	"gonum.org/v1/gonum/stat/distuv"
	"google.golang.org/protobuf/types/known/anypb"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type FaultConfig struct {
	UnixToDaDomainSocketPath   string `yaml:"unix-to-da-domain-socket-path"`
	UnixFromDaDomainSocketPath string `yaml:"unix-from-da-domain-socket-path"`
	FaultsEnabled              bool   `yaml:"faults-enabled"`
	Actions                    struct {
		Noop struct {
			Probability float64 `yaml:"probability"`
		} `yaml:"noop"`
		Halt struct {
			Probability float64 `yaml:"probability"`
			MaxDuration int     `yaml:"max-duration"`
		} `yaml:"halt"`
		Pause struct {
			Probability     float64 `yaml:"probability"`
			MaxDuration     int     `yaml:"max-duration"`
			PauseCommand    string  `yaml:"pause-command"`
			ContinueCommand string  `yaml:"continue-command"`
		} `yaml:"pause"`
		Stop struct {
			Probability    float64 `yaml:"probability"`
			MaxDuration    int     `yaml:"max-duration"`
			StopCommand    string  `yaml:"stop-command"`
			RestartCommand string  `yaml:"restart-command"`
		} `yaml:"stop"`
		ResendLastMessage struct {
			Probability float64 `yaml:"probability"`
			MaxDuration int     `yaml:"max-duration"`
		} `yaml:"resend-last-message"`
	} `yaml:"actions"`
}

type FaultAction interface {
	Perform()
	Name() string
	Type() daproto.ActionType
	GenerateResponse(*daproto.Message) error
}

func ReadFaultConfig(path string) (FaultConfig, error) {
	var config FaultConfig
	content, err := os.ReadFile(path)
	if err != nil {
		return config, err
	}

	err = yaml.Unmarshal(content, &config)
	if err != nil {
		return config, err
	}

	err = config.verifyConfig()
	if err != nil {
		return config, err
	}

	return config, nil
}

func (config *FaultConfig) verifyConfig() error {
	baseErr := errors.New("config error")
	if len(config.UnixToDaDomainSocketPath) == 0 {
		return errors.Join(baseErr, errors.New("unix to DA domain socket path is empty"))
	}

	if len(config.UnixFromDaDomainSocketPath) == 0 {
		return errors.Join(baseErr, errors.New("unix from DA domain socket path is empty"))
	}

	if len(config.Actions.Pause.PauseCommand) == 0 {
		return errors.Join(baseErr, errors.New("pause command is empty"))
	}

	if len(config.Actions.Pause.ContinueCommand) == 0 {
		return errors.Join(baseErr, errors.New("unpause command is empty"))
	}

	if len(config.Actions.Stop.StopCommand) == 0 {
		return errors.Join(baseErr, errors.New("stop command is empty"))
	}

	if len(config.Actions.Stop.RestartCommand) == 0 {
		return errors.Join(baseErr, errors.New("restart command is empty"))
	}

	return nil
}

func (config *FaultConfig) String() (string, error) {
	yamlBytes, err := yaml.Marshal(config)
	if err != nil {
		return "", err
	}

	return string(yamlBytes), nil
}

type ActionPicker struct {
	cumProbabilities []float64
	actions          map[daproto.ActionType]FaultAction
	faultConfig      FaultConfig
}

func NewActionPicker(config FaultConfig) *ActionPicker {
	probabilities := []float64{
		config.Actions.Noop.Probability,
		config.Actions.Halt.Probability,
		config.Actions.Pause.Probability,
		config.Actions.Stop.Probability,
		config.Actions.ResendLastMessage.Probability,
	}
	cumSum := make([]float64, 5, 5)
	floats.CumSum(cumSum, probabilities)

	pauseCmd, pauseArgs := splitCommand(config.Actions.Pause.PauseCommand)
	continueCmd, continueArgs := splitCommand(config.Actions.Pause.ContinueCommand)
	stopCmd, stopArgs := splitCommand(config.Actions.Stop.StopCommand)
	restartCmd, restartArgs := splitCommand(config.Actions.Stop.RestartCommand)
	actions := map[daproto.ActionType]FaultAction{
		daproto.ActionType_NOOP_ACTION_TYPE:                &NoopAction{},
		daproto.ActionType_HALT_ACTION_TYPE:                &HaltAction{config},
		daproto.ActionType_PAUSE_ACTION_TYPE:               &PauseAction{config, pauseCmd, pauseArgs, continueCmd, continueArgs},
		daproto.ActionType_STOP_ACTION_TYPE:                &StopAction{config, stopCmd, stopArgs, restartCmd, restartArgs},
		daproto.ActionType_RESEND_LAST_MESSAGE_ACTION_TYPE: &ResendLastMessageAction{},
	}
	return &ActionPicker{cumProbabilities: cumSum, actions: actions, faultConfig: config}
}

func (actionPicker *ActionPicker) DetermineAction() FaultAction {
	val := distuv.UnitUniform.Rand() * actionPicker.cumProbabilities[len(actionPicker.cumProbabilities)-1]
	actionIdx := sort.Search(len(actionPicker.cumProbabilities), func(i int) bool { return actionPicker.cumProbabilities[i] > val })
	action := actionPicker.actions[daproto.ActionType(actionIdx)]
	DaLogger.Debug("Picking action '%s'", action.Name())
	return action
}

type NoopAction struct {
}

func (action *NoopAction) Type() daproto.ActionType {
	return daproto.ActionType_NOOP_ACTION_TYPE
}

func (action *NoopAction) GenerateResponse(response *daproto.Message) error {
	response.Reset()
	response.MessageType = daproto.MessageType_DA_RESPONSE
	response.MessageObject = &anypb.Any{}
	err := response.MessageObject.MarshalFrom(response)
	if err != nil {
		return err
	}

	return nil
}

func (action *NoopAction) Perform() {
	// Do nothing
}

func (action *NoopAction) Name() string {
	return "Noop"
}

type HaltAction struct {
	config FaultConfig
}

func (action *HaltAction) GenerateResponse(response *daproto.Message) error {
	response.Reset()
	response.MessageType = daproto.MessageType_DA_RESPONSE
	response.MessageObject = &anypb.Any{}
	err := response.MessageObject.MarshalFrom(response)
	if err != nil {
		return err
	}

	return nil
}

func (action *HaltAction) Name() string {
	return "Halt"
}

func (action *HaltAction) Type() daproto.ActionType {
	return daproto.ActionType_HALT_ACTION_TYPE
}

func (action *HaltAction) Perform() {
	// TODO: Introduce randomness
	time.Sleep(time.Duration(action.config.Actions.Halt.MaxDuration) * time.Millisecond)
}

type PauseAction struct {
	config FaultConfig

	pauseCmd  string
	pauseArgs []string

	continueCmd  string
	continueArgs []string
}

func (action *PauseAction) Type() daproto.ActionType {
	return daproto.ActionType_PAUSE_ACTION_TYPE
}

func (action *PauseAction) GenerateResponse(response *daproto.Message) error {
	response.Reset()
	response.MessageType = daproto.MessageType_DA_RESPONSE
	response.MessageObject = &anypb.Any{}
	err := response.MessageObject.MarshalFrom(response)
	if err != nil {
		return err
	}

	return nil
}

func (action *PauseAction) Name() string {
	return "Pause"
}

func (action *PauseAction) Perform() {
	pauseConfig := action.config.Actions.Pause
	err := exec.Command(action.pauseCmd, action.pauseArgs...).Run()
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to execute pause command")
		return
	}

	time.Sleep(time.Duration(pauseConfig.MaxDuration) * time.Millisecond)
	err = exec.Command(action.continueCmd, action.continueArgs...).Run()
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to execute continue command")
		return
	}
}

type StopAction struct {
	config FaultConfig

	stopCmd  string
	stopArgs []string

	restartCmd  string
	restartArgs []string
}

func (action *StopAction) Type() daproto.ActionType {
	return daproto.ActionType_STOP_ACTION_TYPE
}

func (action *StopAction) GenerateResponse(response *daproto.Message) error {
	response.Reset()
	response.MessageType = daproto.MessageType_DA_RESPONSE
	response.MessageObject = &anypb.Any{}
	err := response.MessageObject.MarshalFrom(response)
	if err != nil {
		return err
	}

	return nil
}

func (action *StopAction) Name() string {
	return "Stop"
}

func (action *StopAction) Perform() {
	stopConfig := &action.config.Actions.Stop
	err := exec.Command(action.stopCmd, action.stopArgs...).Run()
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to execute stop command")
		return
	}

	time.Sleep(time.Duration(stopConfig.MaxDuration) * time.Millisecond)
	err = exec.Command(action.restartCmd, action.restartArgs...).Run()
	if err != nil {
		DaLogger.ErrorErr(err, "Failed to execute restart command")
		return
	}
}

type ResendLastMessageAction struct {
}

func (action *ResendLastMessageAction) Type() daproto.ActionType {
	return daproto.ActionType_RESEND_LAST_MESSAGE_ACTION_TYPE
}

func (action *ResendLastMessageAction) GenerateResponse(response *daproto.Message) error {
	response.Reset()
	response.MessageType = daproto.MessageType_DA_RESPONSE
	response.MessageObject = &anypb.Any{}
	err := response.MessageObject.MarshalFrom(response)
	if err != nil {
		return err
	}

	return nil
}

func (action *ResendLastMessageAction) Name() string {
	return "ResendLastMessage"
}

func (action *ResendLastMessageAction) Perform() {

}

func splitCommand(command string) (string, []string) {
	cmdSplit := strings.Split(command, " ")
	cmd := cmdSplit[0]
	var args []string
	if len(cmdSplit) > 1 {
		args = cmdSplit[1:]
	} else {
		args = []string{}
	}

	return cmd, args
}
